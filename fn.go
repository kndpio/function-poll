package main

import (
	"context"
	"encoding/json"
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

// Checking if time passed before sending new message
func timeElapsed(xr *resource.Composite, rsp *fnv1beta1.RunFunctionResponse, logger logging.Logger) bool {
	currentTime := int(time.Now().Unix())
	annotations := xr.Resource.GetAnnotations()
	lastSentMessage, _ := strconv.Atoi(annotations["poll.fn.kndp.io/last-sent-time"])

	if currentTime >= lastSentMessage+900 {
		annotations["poll.fn.kndp.io/last-sent-time"] = strconv.Itoa(currentTime)
		xr.Resource.SetAnnotations(annotations)
		if err := response.SetDesiredCompositeResource(rsp, xr); err != nil {
			logger.Info("error setting desired composite resource", err)
		}
		return true
	}

	return false
}

// Transform K8s resources into unstructured
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
								"serviceAccountName": input.DeploymentServiceAccount,
								"containers": []map[string]interface{}{
									{
										"name":  "poll-container",
										"image": input.DeploymentImage,
										"envFrom": []map[string]interface{}{
											{"configMapRef": map[string]interface{}{"name": input.ConfigMap}},
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

	unstructuredData := composed.Unstructured{}
	unstructuredDataByte, err := json.Marshal(deploymentTemplate)
	if err != nil {
		logger.Info("error marshalling deployment template", "warning", err)
	}
	err = json.Unmarshal(unstructuredDataByte, &unstructuredData)
	if err != nil {
		logger.Info("error unmarshalling deployment template", "warning", err)
	}

	return unstructuredData
}

// Check if the dueOrderTime is passed or all users have voted
func checkDueOrderTimeAndVoteCount(xr *resource.Composite, currentTimestamp int, users []string, logger logging.Logger) bool {
	dueOrderTimeString, _ := xr.Resource.GetString("spec.dueOrderTime")
	dueOrderTime, _ := strconv.Atoi(dueOrderTimeString)
	voters, _ := xr.Resource.GetStringArray("spec.voters")
	logger.Debug("DueOrderTime: ", dueOrderTime, "CurrentTimestamp: ", currentTimestamp)
	if users == nil {
		users = []string{""}
	}

	return currentTimestamp >= dueOrderTime || len(voters) == len(users)
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
	api := slack.New(input.SlackAPIToken)
	currentTimestamp := int(time.Now().Unix())
	desired, _ := request.GetDesiredComposedResources(req)

	rsp := response.To(req, response.DefaultTTL)

	xr, _ := request.GetObservedCompositeResource(req)

	users, err := slackchannel.ProcessSlackMembers(api, input.SlackChanelID, f.log)
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
		if timeElapsed(xr, rsp, f.log) {
			slackchannel.SendSlackMessage(xr, api, input.SlackChanelID, input.SlackNotifyMessage, f.log)
		}

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
