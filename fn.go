package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/crossplane/function-sdk-go/errors"
	"github.com/crossplane/function-sdk-go/logging"
	fnv1beta1 "github.com/crossplane/function-sdk-go/proto/v1beta1"
	"github.com/crossplane/function-sdk-go/request"
	"github.com/crossplane/function-sdk-go/resource"
	"github.com/crossplane/function-sdk-go/resource/composed"
	"github.com/crossplane/function-sdk-go/response"
	"github.com/slack-go/slack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Poll struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		DeliveryTime string        `json:"deliveryTime"`
		DueOrderTime string        `json:"dueOrderTime"`
		DueTakeTime  string        `json:"dueTakeTime"`
		EmployeeRefs []EmployeeRef `json:"employeeRefs"`
		Status       string        `json:"status"`
	} `json:"spec"`
}

type EmployeeRef struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type Unstructured struct {
	APIVersion string                 `json:"apiVersion"`
	Kind       string                 `json:"kind"`
	Metadata   map[string]interface{} `json:"metadata"`
	Spec       map[string]interface{} `json:"spec"`
}

// Function returns whatever response you ask it to.
type Function struct {
	fnv1beta1.UnimplementedFunctionRunnerServiceServer
	log logging.Logger
}

var api = slack.New(os.Getenv("SLACK_API_TOKEN"))
var channelID = os.Getenv("SLACK_NOTIFY_CHANNEL_ID")

// RunFunction adds a Deployment and the new object template to the desired state.
func (f *Function) RunFunction(_ context.Context, req *fnv1beta1.RunFunctionRequest) (*fnv1beta1.RunFunctionResponse, error) {
	rsp := response.To(req, response.DefaultTTL)
	xr, err := request.GetObservedCompositeResource(req)
	if err != nil {
		response.Fatal(rsp, errors.Wrapf(err, "cannot get observed composite resource from %T", req))
		return rsp, nil
	}

	members, _, err := api.GetUsersInConversation(&slack.GetUsersInConversationParameters{
		ChannelID: channelID,
	})
	if err != nil {
		log.Fatalf("Error getting conversation members: %v", err)
	}

	realUsers := make([]string, 0)
	for _, memberID := range members {
		// Check if user is not a bot
		userInfo, err := api.GetUserInfo(memberID)
		if err != nil {
			log.Printf("Error getting user info for %s: %v", memberID, err)
			continue
		}

		if !userInfo.IsBot {
			realUsers = append(realUsers, userInfo.Name)
		}
	}

	employers, _ := xr.Resource.GetStringArray("spec.employeeRefs")
	dueOrderTimeString, _ := xr.Resource.GetString("spec.dueOrderTime")
	dueOrderTime, _ := strconv.Atoi(dueOrderTimeString)
	currentTimestamp := int(time.Now().Unix())
	fmt.Println("Votes: ", len(employers), "from ", len(realUsers))
	fmt.Println("DueOrderTime: ", dueOrderTime, "CurrentTimestamp: ", currentTimestamp)
	if currentTimestamp >= dueOrderTime || len(employers) == len(realUsers) {

		jobTemplate := map[string]interface{}{
			"apiVersion": "kubernetes.crossplane.io/v1alpha1",
			"kind":       "Object",
			"metadata": map[string]interface{}{
				"name": os.Getenv("JOB_NAME"),
			},
			"spec": map[string]interface{}{
				"forProvider": map[string]interface{}{
					"manifest": map[string]interface{}{
						"apiVersion": "batch/v1",
						"kind":       "Job",
						"metadata": map[string]interface{}{
							"name":      os.Getenv("JOB_NAME"),
							"namespace": "default",
						},
						"spec": map[string]interface{}{
							"template": map[string]interface{}{
								"spec": map[string]interface{}{
									"serviceAccountName": os.Getenv("JOB_SERVICE_ACCOUNT_NAME"),
									"containers": []map[string]interface{}{
										{
											"name":  "poll-container",
											"image": os.Getenv("JOB_IMAGE_NAME"),
											"envFrom": []map[string]interface{}{
												{"configMapRef": map[string]interface{}{"name": "poll-cm"}},
											},
										},
									},
									"restartPolicy": "OnFailure",
								},
							},
						},
					},
				},
				"managementPolicy":  "Default",
				"providerConfigRef": map[string]interface{}{"name": os.Getenv("PROVIDER_CONFIG_REF_NAME")},
			},
		}

		unstructuredData := composed.Unstructured{}
		unstructuredDataByte, _ := json.Marshal(jobTemplate)
		json.Unmarshal(unstructuredDataByte, &unstructuredData)
		desired, err := request.GetDesiredComposedResources(req)

		if err != nil {
			response.Fatal(rsp, errors.Wrapf(err, "cannot get desired resources from %T", req))
			return rsp, nil
		}
		desired[resource.Name(jobTemplate["metadata"].(map[string]interface{})["name"].(string))] = &resource.DesiredComposed{Resource: &unstructuredData}

		if err := response.SetDesiredComposedResources(rsp, desired); err != nil {
			response.Fatal(rsp, errors.Wrapf(err, "cannot set desired composed resources in %T", rsp))
			return rsp, nil
		}

		fmt.Println("DueOrderTime is passed due, send results in slack chanel")
	} else {
		desired, err := request.GetDesiredComposedResources(req)
		if err != nil {
			response.Fatal(rsp, errors.Wrapf(err, "cannot get desired resources from %T", req))
			return rsp, nil
		}
		pollServicePort, _ := strconv.Atoi(os.Getenv("POLL_SERVICE_PORT"))
		pollServiceTargetPort, _ := strconv.Atoi(os.Getenv("POLL_SERVICE_TARGET_PORT"))
		ingressTemplate := map[string]interface{}{
			"apiVersion": "kubernetes.crossplane.io/v1alpha1",
			"kind":       "Object",
			"metadata": map[string]interface{}{
				"name": os.Getenv("INGRESS_NAME"),
			},
			"spec": map[string]interface{}{
				"forProvider": map[string]interface{}{
					"manifest": map[string]interface{}{
						"apiVersion": "networking.k8s.io/v1",
						"kind":       "Ingress",
						"metadata": map[string]interface{}{
							"name":      os.Getenv("INGRESS_NAME"),
							"namespace": "default",
						},
						"spec": map[string]interface{}{
							"rules": []map[string]interface{}{
								{
									"host": os.Getenv("HOST_NAME"),
									"http": map[string]interface{}{
										"paths": []map[string]interface{}{
											{
												"pathType": "Prefix",
												"path":     "/",
												"backend": map[string]interface{}{
													"service": map[string]interface{}{
														"name": os.Getenv("POLL_SERVICE_NAME"),
														"port": map[string]interface{}{
															"number": pollServicePort,
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
				"managementPolicy":  "Default",
				"providerConfigRef": map[string]interface{}{"name": os.Getenv("PROVIDER_CONFIG_REF_NAME")},
			},
		}

		serviceTemplate := map[string]interface{}{
			"apiVersion": "kubernetes.crossplane.io/v1alpha1",
			"kind":       "Object",
			"metadata": map[string]interface{}{
				"name": os.Getenv("POLL_SERVICE_NAME"),
			},
			"spec": map[string]interface{}{
				"forProvider": map[string]interface{}{
					"manifest": map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Service",
						"metadata": map[string]interface{}{
							"name":      os.Getenv("POLL_SERVICE_NAME"),
							"namespace": "default",
						},
						"spec": map[string]interface{}{
							"selector": map[string]interface{}{
								"app": "poll",
							},
							"ports": []map[string]interface{}{
								{
									"protocol":   "TCP",
									"port":       pollServicePort,
									"targetPort": pollServiceTargetPort,
								},
							},
							"type": "ClusterIP",
						},
					},
				},
				"managementPolicy":  "Default",
				"providerConfigRef": map[string]interface{}{"name": os.Getenv("PROVIDER_CONFIG_REF_NAME")},
			},
		}

		cronJobTemplate := map[string]interface{}{
			"apiVersion": "kubernetes.crossplane.io/v1alpha1",
			"kind":       "Object",
			"metadata": map[string]interface{}{
				"name": os.Getenv("CRONJOB_NAME"),
			},
			"spec": map[string]interface{}{
				"forProvider": map[string]interface{}{
					"manifest": map[string]interface{}{
						"apiVersion": "batch/v1",
						"kind":       "CronJob",
						"metadata": map[string]interface{}{
							"name":      os.Getenv("CRONJOB_NAME"),
							"namespace": "default",
						},
						"spec": map[string]interface{}{
							"schedule": os.Getenv("CRONJOB_SCHEDULE_TIME"),
							"jobTemplate": map[string]interface{}{
								"spec": map[string]interface{}{
									"template": map[string]interface{}{
										"spec": map[string]interface{}{
											"serviceAccountName": os.Getenv("CRONJOB_SERVICE_ACCOUNT_NAME"),
											"containers": []map[string]interface{}{
												{
													"name":  "poll-container",
													"image": os.Getenv("CRONJOB_IMAGE_NAME"),
													"envFrom": []map[string]interface{}{
														{"configMapRef": map[string]interface{}{"name": "poll-cm"}},
													},
												},
											},
											"restartPolicy": "OnFailure",
										},
									},
								},
							},
						},
					},
				},
				"managementPolicy":  "Default",
				"providerConfigRef": map[string]interface{}{"name": os.Getenv("PROVIDER_CONFIG_REF_NAME")},
			},
		}

		deploymentTemplate := map[string]interface{}{
			"apiVersion": "kubernetes.crossplane.io/v1alpha1",
			"kind":       "Object",
			"metadata": map[string]interface{}{
				"name": os.Getenv("DEPLOYMENT_NAME"),
			},
			"spec": map[string]interface{}{
				"forProvider": map[string]interface{}{
					"manifest": map[string]interface{}{
						"apiVersion": "apps/v1",
						"kind":       "Deployment",
						"metadata": map[string]interface{}{
							"name":      os.Getenv("DEPLOYMENT_NAME"),
							"namespace": "default",
						},
						"spec": map[string]interface{}{
							"replicas": 1,
							"selector": map[string]interface{}{
								"matchLabels": map[string]interface{}{
									"app": "poll",
								},
							},
							"template": map[string]interface{}{
								"metadata": map[string]interface{}{
									"labels": map[string]interface{}{
										"app": "poll",
									},
								},
								"spec": map[string]interface{}{
									"serviceAccountName": os.Getenv("DEPLOYMENT_SERVICE_ACCOUNT_NAME"),
									"containers": []map[string]interface{}{
										{
											"name":  "poll-container",
											"image": os.Getenv("DEPLOYMENT_IMAGE_NAME"),
											"envFrom": []map[string]interface{}{
												{"configMapRef": map[string]interface{}{"name": os.Getenv("CONFIG_MAP_NAME")}},
											},
											"ports": []map[string]interface{}{
												{
													"containerPort": 80,
												},
											},
										},
									},
								},
							},
						},
					},
				},
				"managementPolicy":  "Default",
				"providerConfigRef": map[string]interface{}{"name": os.Getenv("PROVIDER_CONFIG_REF_NAME")},
			},
		}

		// List of templates
		templates := []map[string]interface{}{deploymentTemplate, ingressTemplate, cronJobTemplate, serviceTemplate}

		// Process each template
		for _, template := range templates {
			unstructuredData := composed.Unstructured{}
			unstructuredDataByte, err := json.Marshal(template)
			if err != nil {
				response.Fatal(rsp, errors.Wrapf(err, "error marshaling Unstructured data: %s", err))
				return rsp, nil
			}
			json.Unmarshal(unstructuredDataByte, &unstructuredData)
			desired[resource.Name(template["metadata"].(map[string]interface{})["name"].(string))] = &resource.DesiredComposed{Resource: &unstructuredData}
		}

		if err := response.SetDesiredComposedResources(rsp, desired); err != nil {
			response.Fatal(rsp, errors.Wrapf(err, "cannot set desired composed resources in %T", rsp))
			return rsp, nil
		}

		f.log.Info("Added Deployment, Ingress, and CronJob templates to desired state")

	}
	return rsp, nil
}
