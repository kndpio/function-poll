package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/slack-go/slack"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	v1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/function-sdk-go/logging"
	fnv1beta1 "github.com/crossplane/function-sdk-go/proto/v1beta1"
	"github.com/crossplane/function-sdk-go/request"
	"github.com/crossplane/function-sdk-go/resource"
	"github.com/crossplane/function-sdk-go/resource/composed"
	"github.com/crossplane/function-sdk-go/response"
	"github.com/crossplane/function-template-go/input/v1beta1"
)

// Poll type
type Poll struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		DeliveryTime string  `json:"deliveryTime"`
		DueOrderTime string  `json:"dueOrderTime"`
		DueTakeTime  string  `json:"dueTakeTime"`
		Voters       []Voter `json:"voters"`
		Status       string  `json:"status"`
	} `json:"spec"`
}

// Voter type
type Voter struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type initialResources struct {
	Input            *v1beta1.Input
	API              *slack.Client
	XR               *resource.Composite
	Rsp              *fnv1beta1.RunFunctionResponse
	CurrentTimestamp int
	Desired          map[resource.Name]*resource.DesiredComposed
}

// Function returns whatever response you ask it to.
type Function struct {
	fnv1beta1.UnimplementedFunctionRunnerServiceServer
	log logging.Logger
}

// Checking if time passed before sending new message
func timeElapsed(xr *resource.Composite, rsp *fnv1beta1.RunFunctionResponse) bool {
	currentTime := int(time.Now().Unix())
	annotations := xr.Resource.GetAnnotations()
	lastSentMessage, _ := strconv.Atoi(annotations["poll.fn.kndp.io/last-sent-time"])

	if currentTime >= lastSentMessage+900 {
		annotations["poll.fn.kndp.io/last-sent-time"] = strconv.Itoa(currentTime)
		xr.Resource.SetAnnotations(annotations)
		if err := response.SetDesiredCompositeResource(rsp, xr); err != nil {
			fmt.Println(err)
		}
		return true
	}

	return false
}

// Check if the user should receive a message based on status
func userVoted(voters []Voter, userName string) bool {
	for _, Voter := range voters {
		if Voter.Name == userName {
			return Voter.Status == ""
		}
	}
	return true
}

// Function for sending messages to users in slack
func sendSlackMessage(xr *resource.Composite, api *slack.Client, channelID string, slackNotifyMessage string) {
	members, _, err := api.GetUsersInConversation(&slack.GetUsersInConversationParameters{
		ChannelID: channelID,
	})
	if err != nil {
		fmt.Printf("Error getting conversation members: %v", err)
	}

	fmt.Println("Conversation Members:", members)
	pollName, _ := xr.Resource.GetString("metadata.name")
	pollTitle, _ := xr.Resource.GetString("spec.Title")
	poll := Poll{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(xr.Resource.Object, &poll); err != nil {
		fmt.Println("error converting Unstructured to Poll:", err)
	}

	for _, memberID := range members {
		userInfo, err := api.GetUserInfo(memberID)
		if err != nil {
			log.Printf("Error getting user info for %s: %v", memberID, err)
			continue
		}

		attachment := slack.Attachment{
			Color:      "#f9a41b",
			CallbackID: pollName,
			Title:      pollTitle,
			TitleLink:  pollTitle,
			Text:       slackNotifyMessage,
			Fields:     []slack.AttachmentField{},
			Actions:    []slack.AttachmentAction{{Name: "actionSelect", Type: "select", Options: []slack.AttachmentActionOption{{Text: "Yes", Value: "Yes"}, {Text: "No", Value: "No"}}}, {Name: "actionCancel", Text: "Cancel", Type: "button", Style: "danger"}},
			MarkdownIn: []string{},
			Blocks:     slack.Blocks{},
		}

		if userVoted(poll.Spec.Voters, userInfo.Name) {
			channelID, _, err := api.PostMessage(
				userInfo.ID,
				slack.MsgOptionText("", true),
				slack.MsgOptionAttachments(attachment),
				slack.MsgOptionAsUser(true),
			)
			if err != nil {
				log.Printf("Error sending message to user %s (%s): %v", userInfo.Name, userInfo.ID, err)
				continue
			}

			fmt.Printf("Message sent to user %s (%s) in channel %s\n", userInfo.Name, userInfo.ID, channelID)
		}
	}
}

// Transform K8s resources into unstructured
func (f *Function) transformK8sResource(input *v1beta1.Input) composed.Unstructured {

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
		fmt.Println(err)
	}
	err = json.Unmarshal(unstructuredDataByte, &unstructuredData)
	if err != nil {
		fmt.Println(err)
	}

	return unstructuredData
}

// Get initial resources
func getInitialResources(req *fnv1beta1.RunFunctionRequest) (*initialResources, error) {
	input := &v1beta1.Input{}
	if err := request.GetInput(req, input); err != nil {
		fmt.Println(err)
	}
	desired, err := request.GetDesiredComposedResources(req)
	if err != nil {
		return nil, err
	}
	api := slack.New(input.SlackAPIToken)
	rsp := response.To(req, response.DefaultTTL)

	xr, err := request.GetObservedCompositeResource(req)
	if err != nil {
		return nil, err
	}
	return &initialResources{Input: input, API: api, XR: xr, Rsp: rsp, Desired: desired, CurrentTimestamp: int(time.Now().Unix())}, nil
}

// Get and process slack members
func processSlackMembers(api *slack.Client, channelID string) ([]string, error) {
	members, _, err := api.GetUsersInConversation(&slack.GetUsersInConversationParameters{ChannelID: channelID})
	if err != nil {
		return nil, err
	}
	realUsers := make([]string, 0)
	for _, memberID := range members {
		userInfo, err := api.GetUserInfo(memberID)
		if err != nil {
			log.Printf("Error getting user info for %s: %v", memberID, err)
			continue
		}

		if !userInfo.IsBot {
			realUsers = append(realUsers, userInfo.Name)
		}
	}
	return realUsers, nil
}

// Check if the dueOrderTime is passed or all users have voted
func checkDueOrderTimeAndVoteCount(xr *resource.Composite, currentTimestamp int, users []string) bool {
	dueOrderTimeString, _ := xr.Resource.GetString("spec.dueOrderTime")
	dueOrderTime, _ := strconv.Atoi(dueOrderTimeString)
	voters, _ := xr.Resource.GetStringArray("spec.voters")
	fmt.Println("DueOrderTime: ", dueOrderTime, "CurrentTimestamp: ", currentTimestamp)
	return currentTimestamp >= dueOrderTime || len(voters) == len(users)
}

// RunFunction adds a Deployment and the new object template to the desired state.
func (f *Function) RunFunction(_ context.Context, req *fnv1beta1.RunFunctionRequest) (*fnv1beta1.RunFunctionResponse, error) {

	initialResources, err := getInitialResources(req)
	if err != nil {
		return initialResources.Rsp, err
	}

	var conditionStatus corev1.ConditionStatus

	users, err := processSlackMembers(initialResources.API, initialResources.Input.SlackChanelID)
	if err != nil {
		fmt.Printf("Error getting conversation members: %v", err)
	}

	if checkDueOrderTimeAndVoteCount(initialResources.XR, initialResources.CurrentTimestamp, users) {
		conditionStatus = corev1.ConditionTrue
		fmt.Println("DueOrderTime is passed due, send results in slack chanel")
		initialResources.XR.Resource.SetConditions(v1.Condition{Type: v1.TypeSynced, Status: corev1.ConditionTrue})
		err = response.SetDesiredCompositeResource(initialResources.Rsp, initialResources.XR)
		if err != nil {
			return initialResources.Rsp, err
		}

	} else {
		conditionStatus = corev1.ConditionFalse
		if timeElapsed(initialResources.XR, initialResources.Rsp) {
			sendSlackMessage(initialResources.XR, initialResources.API, initialResources.Input.SlackChanelID, initialResources.Input.SlackNotifyMessage)
		}

		deployment := f.transformK8sResource(initialResources.Input)
		initialResources.Desired[resource.Name(deployment.GetName())] = &resource.DesiredComposed{Resource: &deployment}

		if err := response.SetDesiredComposedResources(initialResources.Rsp, initialResources.Desired); err != nil {
			return initialResources.Rsp, err
		}
	}
	initialResources.XR.Resource.SetConditions(v1.Condition{Type: v1.TypeSynced, Status: conditionStatus})
	err = response.SetDesiredCompositeResource(initialResources.Rsp, initialResources.XR)
	if err != nil {
		return initialResources.Rsp, err
	}
	return initialResources.Rsp, nil
}
