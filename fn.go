package main

import (
	"context"
	"os"
	"time"

	"github.com/slack-go/slack"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/crossplane/function-sdk-go/logging"
	fnv1beta1 "github.com/crossplane/function-sdk-go/proto/v1beta1"
	"github.com/crossplane/function-sdk-go/request"
	"github.com/crossplane/function-sdk-go/resource"
	"github.com/crossplane/function-sdk-go/resource/composed"
	"github.com/crossplane/function-sdk-go/response"
	"github.com/crossplane/function-template-go/input/v1beta1"
	"github.com/crossplane/function-template-go/internal/slackchannel"
)

// Function returns whatever response you ask it to.
type Function struct {
	fnv1beta1.UnimplementedFunctionRunnerServiceServer
	log logging.Logger
}

var (
	token           = os.Getenv("SLACK_API_TOKEN")
	channelID       = os.Getenv("SLACK_CHANEL_ID")
	secretName      = os.Getenv("SECRET_NAME")
	ngrokDomainName = os.Getenv("NGROK_DOMAIN_NAME")
)

// Check if the dueOrderTime is passed or all users have voted
func checkDueOrderTimeAndVoteCount(xr *resource.Composite, currentTimestamp int, users []string) bool {
	dueOrderTime, _ := xr.Resource.GetInteger("spec.dueOrderTime")
	lastNotificationTime, _ := xr.Resource.GetInteger("status.lastNotificationTime")
	voters, _ := xr.Resource.GetStringArray("spec.voters")
	if users == nil {
		users = []string{""}
	}
	if lastNotificationTime == 0 {
		return false
	}
	timestampDue := int(lastNotificationTime) + int(dueOrderTime)
	return currentTimestamp >= timestampDue || len(voters) == len(users)

}

// RunFunction adds a Deployment and the new object template to the desired state.
func (f *Function) RunFunction(_ context.Context, req *fnv1beta1.RunFunctionRequest) (*fnv1beta1.RunFunctionResponse, error) {
	f.log.Info("Running Function")
	input := &v1beta1.Input{}
	if err := request.GetInput(req, input); err != nil {
		f.log.Info("cannot get function input", "warning", err)
	}
	api := slack.New(token)
	currentTimestamp := int(time.Now().Unix())
	desired, _ := request.GetDesiredComposedResources(req)

	rsp := response.To(req, response.DefaultTTL)

	xr, _ := request.GetObservedCompositeResource(req)
	users, err := slackchannel.ProcessSlackMembers(api, channelID, f.log)
	if err != nil {
		f.log.Info("cannot get conversation members", "warning", err)
	}
	pollTitle, _ := xr.Resource.GetString("spec.title")
	pollName, _ := xr.Resource.GetString("metadata.name")
	schedule, _ := xr.Resource.GetString("spec.schedule")
	question, _ := xr.Resource.GetString("spec.messages.question")
	d, _ := xr.Resource.GetBool("status.done")
	resultText, _ := xr.Resource.GetString("spec.messages.result")

	if checkDueOrderTimeAndVoteCount(xr, currentTimestamp, users) {
		if !d {
			xr.Resource.SetBool("status.done", true)
			slackchannel.SlackOrder(input, api, xr, f.log, resultText)
		}
		xr.Resource.SetManagedFields(nil)
		response.SetDesiredCompositeResource(rsp, xr)

	} else {
		xr.Resource.SetManagedFields(nil)
		response.SetDesiredCompositeResource(rsp, xr)

		deployment := composed.Unstructured{
			Unstructured: unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "kubernetes.crossplane.io/v1alpha2",
					"kind":       "Object",
					"metadata": map[string]interface{}{
						"name": input.DeploymentName,
					},
					"spec": map[string]interface{}{
						"forProvider": map[string]interface{}{
							"manifest": map[string]interface{}{
								"apiVersion": "apps/v1",
								"kind":       "Deployment",
								"metadata": map[string]interface{}{
									"name":      input.DeploymentName,
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
											"serviceAccountName": input.ServiceAccountName,
											"containers": []interface{}{
												map[string]interface{}{
													"name":  "poll-container",
													"image": input.DeploymentImage,
													"envFrom": []interface{}{
														map[string]interface{}{
															"secretRef": map[string]interface{}{
																"name": secretName + "creds",
															},
														},
													},
													"ports": []interface{}{
														map[string]interface{}{
															"containerPort": 3000,
														},
													},
												},
											},
										},
									},
								},
							},
						},
						"providerConfigRef": map[string]interface{}{"name": input.ProviderConfigRef},
					},
				},
			},
		}

		svc := composed.Unstructured{
			Unstructured: unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "kubernetes.crossplane.io/v1alpha2",
					"kind":       "Object",
					"metadata": map[string]interface{}{
						"name": "service-collector",
					},
					"spec": map[string]interface{}{
						"forProvider": map[string]interface{}{
							"manifest": map[string]interface{}{
								"apiVersion": "v1",
								"kind":       "Service",
								"metadata": map[string]interface{}{
									"name":      "service-collector",
									"namespace": "default",
								},
								"spec": map[string]interface{}{
									"ports": []interface{}{
										map[string]interface{}{
											"name":       "http",
											"port":       80,
											"targetPort": 3000,
										},
									},
									"selector": map[string]interface{}{
										"app": "poll",
									},
								},
							},
						},
						"providerConfigRef": map[string]interface{}{"name": input.ProviderConfigRef},
					},
				},
			},
		}

		ingress := composed.Unstructured{
			Unstructured: unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "kubernetes.crossplane.io/v1alpha2",
					"kind":       "Object",
					"metadata": map[string]interface{}{
						"name": "ingress-collector",
					},
					"spec": map[string]interface{}{
						"forProvider": map[string]interface{}{
							"manifest": map[string]interface{}{
								"apiVersion": "networking.k8s.io/v1",
								"kind":       "Ingress",
								"metadata": map[string]interface{}{
									"name":      "collector",
									"namespace": "default",
								},
								"spec": map[string]interface{}{
									"ingressClassName": "ngrok",
									"rules": []interface{}{
										map[string]interface{}{
											"host": ngrokDomainName,
											"http": map[string]interface{}{
												"paths": []interface{}{
													map[string]interface{}{
														"path":     "/events",
														"pathType": "Prefix",
														"backend": map[string]interface{}{
															"service": map[string]interface{}{
																"name": "service-collector",
																"port": map[string]interface{}{
																	"number": 80,
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
						"providerConfigRef": map[string]interface{}{"name": input.ProviderConfigRef},
					},
				},
			},
		}

		desired[resource.Name(deployment.GetName())] = &resource.DesiredComposed{Resource: &deployment}
		desired[resource.Name(svc.GetName())] = &resource.DesiredComposed{Resource: &svc}
		desired[resource.Name(ingress.GetName())] = &resource.DesiredComposed{Resource: &ingress}

	}

	cronjob := composed.Unstructured{
		Unstructured: unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "kubernetes.crossplane.io/v1alpha2",
				"kind":       "Object",
				"metadata": map[string]interface{}{
					"name": "slack-notify-cronjob",
				},
				"spec": map[string]interface{}{
					"forProvider": map[string]interface{}{
						"manifest": map[string]interface{}{
							"apiVersion": "batch/v1",
							"kind":       "CronJob",
							"metadata": map[string]interface{}{
								"name":      "slack-notify-cronjob",
								"namespace": "default",
							},
							"spec": map[string]interface{}{
								"schedule": schedule,
								"timeZone": "Europe/Chisinau",
								"jobTemplate": map[string]interface{}{
									"spec": map[string]interface{}{
										"template": map[string]interface{}{
											"spec": map[string]interface{}{
												"restartPolicy":      "OnFailure",
												"serviceAccountName": input.ServiceAccountName,
												"containers": []interface{}{
													map[string]interface{}{
														"name":  "poll-container",
														"image": input.CronJobImage,
														"env": []interface{}{
															map[string]interface{}{
																"name":  "SLACK_NOTIFY_MESSAGE",
																"value": question,
															},
															map[string]interface{}{
																"name":  "POLL_NAME",
																"value": pollName,
															},
															map[string]interface{}{
																"name":  "POLL_TITLE",
																"value": pollTitle,
															},
														},
														"envFrom": []interface{}{
															map[string]interface{}{
																"secretRef": map[string]interface{}{
																	"name": secretName + "creds",
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
					},
					"providerConfigRef": map[string]interface{}{"name": input.ProviderConfigRef},
				},
			},
		},
	}
	desired[resource.Name(cronjob.GetName())] = &resource.DesiredComposed{Resource: &cronjob}

	if err := response.SetDesiredComposedResources(rsp, desired); err != nil {
		return rsp, err
	}
	return rsp, nil
}
