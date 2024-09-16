package main

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"time"

	"github.com/slack-go/slack"
	corev1 "k8s.io/api/core/v1"

	v1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
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
	token      = os.Getenv("SLACK_API_TOKEN")
	channelID  = os.Getenv("SLACK_CHANEL_ID")
	secretName = os.Getenv("SECRET_NAME")
)

// Transform K8s resources into unstructured
// It must create all resources that are needed for the deployment to work like rbac,   etc
func (f *Function) transformK8sResource(input *v1beta1.Input, logger logging.Logger) composed.Unstructured {
	deploymentTemplate := map[string]interface{}{
		"apiVersion": "kubernetes.crossplane.io/v1alpha1",
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
								"serviceAccountName": input.DeploymentName,
								"containers": []map[string]interface{}{
									{
										"name":  "poll-container",
										"image": input.DeploymentImage,
										"envFrom": []map[string]interface{}{
											{"secretRef": map[string]interface{}{"name": secretName}},
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
			"providerConfigRef": map[string]interface{}{"name": input.ProviderConfigRef},
		},
	}

	var clusterRole = map[string]interface{}{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRole",
		"metadata": map[string]interface{}{
			"name": "poll-cluster-role",
		},
		"rules": []interface{}{
			map[string]interface{}{
				"apiGroups": []interface{}{"apps"},
				"resources": []interface{}{"deployments"},
				"verbs":     []interface{}{"*"},
			},
			map[string]interface{}{
				"apiGroups": []interface{}{"batch"},
				"resources": []interface{}{"cronjobs"},
				"verbs":     []interface{}{"*"},
			},
			map[string]interface{}{
				"apiGroups": []interface{}{"networking.k8s.io"},
				"resources": []interface{}{"ingresses"},
				"verbs":     []interface{}{"*"},
			},
			map[string]interface{}{
				"apiGroups": []interface{}{"kndp.io"},
				"resources": []interface{}{"polls"},
				"verbs":     []interface{}{"*"},
			},
			map[string]interface{}{
				"apiGroups": []interface{}{"v1"},
				"resources": []interface{}{"services"},
				"verbs":     []interface{}{"*"},
			},
		},
	}

	var serviceAccount = map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ServiceAccount",
		"metadata": map[string]interface{}{
			"name": input.DeploymentName,
		},
	}

	var clusterRoleBinding = map[string]interface{}{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRoleBinding",
		"metadata": map[string]interface{}{
			"name": "poll-cluster-role-binding",
		},
		"subjects": []interface{}{
			map[string]interface{}{
				"kind":      "ServiceAccount",
				"name":      input.DeploymentName,
				"namespace": "default",
			},
		},
		"roleRef": map[string]interface{}{
			"kind":     "ClusterRole",
			"name":     "poll-cluster-role",
			"apiGroup": "rbac.authorization.k8s.io",
		},
	}

	unstructuredData := composed.Unstructured{}
	for _, resource := range []map[string]interface{}{clusterRole, serviceAccount, clusterRoleBinding, deploymentTemplate} {
		unstructuredDataByte, err := json.Marshal(resource)
		if err != nil {
			logger.Info("error marshalling resource", "warning", err)
		}
		err = json.Unmarshal(unstructuredDataByte, &unstructuredData)
		if err != nil {
			logger.Info("error unmarshalling resource", "warning", err)
		}
	}

	return unstructuredData
}

// Check if the dueOrderTime is passed or all users have voted
func checkDueOrderTimeAndVoteCount(xr *resource.Composite, currentTimestamp int, users []string, logger logging.Logger) bool {
	dueOrderTimeString, _ := xr.Resource.GetString("spec.dueOrderTime")
	dueOrderTime, _ := strconv.Atoi(dueOrderTimeString)
	voters, _ := xr.Resource.GetStringArray("spec.voters")
	if users == nil {
		users = []string{""}
	}

	creationTimestamp, _ := xr.Resource.GetString("metadata.creationTimestamp")
	parsedTime, _ := time.Parse(time.RFC3339, creationTimestamp)
	timestampDue := int(parsedTime.Unix()) + dueOrderTime
	return currentTimestamp >= timestampDue || len(voters) == len(users)
}

func setSyncedCondition(xr *resource.Composite, conditionStatus corev1.ConditionStatus) {
	xr.Resource.SetConditions(v1.Condition{Type: v1.TypeSynced, Status: conditionStatus})
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
	if checkDueOrderTimeAndVoteCount(xr, currentTimestamp, users, f.log) {
		setSyncedCondition(xr, corev1.ConditionTrue)
		err = response.SetDesiredCompositeResource(rsp, xr)
		if err != nil {
			return rsp, err
		}
		status, _ := xr.Resource.GetString("spec.status")
		if status != "done" {
			slackchannel.SlackOrder(input, api, xr, f.log)
		}
		err := xr.Resource.SetString("spec.status", "done")
		if err != nil {
			return rsp, err
		}
	} else {
		setSyncedCondition(xr, corev1.ConditionFalse)
		q, _ := xr.Resource.GetString("spec.messages.question")
		slackchannel.SendSlackMessage(xr, api, channelID, q, f.log)

		deployment := f.transformK8sResource(input, f.log)
		desired[resource.Name(deployment.GetName())] = &resource.DesiredComposed{Resource: &deployment}

		if err := response.SetDesiredComposedResources(rsp, desired); err != nil {
			return rsp, err
		}
	}
	err = response.SetDesiredCompositeResource(rsp, xr)
	if err != nil {
		return rsp, err
	}
	return rsp, nil
}
