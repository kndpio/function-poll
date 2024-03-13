package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/crossplane/function-sdk-go/logging"
	fnv1beta1 "github.com/crossplane/function-sdk-go/proto/v1beta1"
	"github.com/crossplane/function-sdk-go/request"
	"github.com/crossplane/function-sdk-go/resource"
	"github.com/crossplane/function-sdk-go/resource/composed"
	"github.com/crossplane/function-sdk-go/response"
	"github.com/crossplane/function-template-go/input/v1beta1"
	"github.com/slack-go/slack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

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

type Voter struct {
	Name   string `json:"name"`
	Status string `json:"status"`
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

	if annotations["poll.fn.kndp.io/last-sent-time"] == "" || currentTime >= lastSentMessage+900 {
		if annotations["poll.fn.kndp.io/last-sent-time"] == "" {
			annotations["poll.fn.kndp.io/last-sent-time"] = strconv.Itoa(currentTime)
		}
		lastSentMessage = currentTime
		annotations["poll.fn.kndp.io/last-sent-time"] = strconv.Itoa(lastSentMessage)
		xr.Resource.SetAnnotations(annotations)
		err := response.SetDesiredCompositeResource(rsp, xr)
		if err != nil {
			fmt.Println(err)
		}
		return true
	}
	xr.Resource.SetAnnotations(annotations)
	err := response.SetDesiredCompositeResource(rsp, xr)
	if err != nil {
		fmt.Println(err)
	}
	return false
}

// Check if the user should receive a message based on status
func userVoted(Voters []Voter, userName string) bool {
	for _, Voter := range Voters {
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
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(xr.Resource.Object, poll); err != nil {
		fmt.Println("error converting Unstructured to Meal:", err)
	}

	for _, memberID := range members {
		userInfo, err := api.GetUserInfo(memberID)
		if err != nil {
			log.Printf("Error getting user info for %s: %v", memberID, err)
			continue
		}

		attachment := slack.Attachment{
			Color:         "#f9a41b",
			Fallback:      "",
			CallbackID:    pollName,
			AuthorID:      "",
			AuthorName:    "",
			AuthorSubname: "",
			AuthorLink:    "",
			AuthorIcon:    "",
			Title:         pollTitle,
			TitleLink:     pollTitle,
			Pretext:       "",
			Text:          slackNotifyMessage,
			ImageURL:      "",
			ThumbURL:      "",
			ServiceName:   "",
			ServiceIcon:   "",
			FromURL:       "",
			OriginalURL:   "",
			Fields:        []slack.AttachmentField{},
			Actions:       []slack.AttachmentAction{{Name: "actionSelect", Type: "select", Options: []slack.AttachmentActionOption{{Text: "Yes", Value: "Yes"}, {Text: "No", Value: "No"}}}, {Name: "actionCancel", Text: "Cancel", Type: "button", Style: "danger"}},
			MarkdownIn:    []string{},
			Blocks:        slack.Blocks{},
			Footer:        "",
			FooterIcon:    "",
			Ts:            "",
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

// RunFunction adds a Deployment and the new object template to the desired state.
func (f *Function) RunFunction(_ context.Context, req *fnv1beta1.RunFunctionRequest) (*fnv1beta1.RunFunctionResponse, error) {

	input := &v1beta1.Input{}
	if err := request.GetInput(req, input); err != nil {
		fmt.Println(err)
	}

	api := slack.New(input.SlackApiToken)
	rsp := response.To(req, response.DefaultTTL)
	xr, err := request.GetObservedCompositeResource(req)
	if err != nil {
		fmt.Println(err)
		return rsp, nil
	}

	members, _, err := api.GetUsersInConversation(&slack.GetUsersInConversationParameters{
		ChannelID: input.SlackChanelID,
	})
	if err != nil {
		fmt.Printf("Error getting conversation members: %v", err)
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

	Voters, _ := xr.Resource.GetStringArray("spec.voters")
	dueOrderTimeString, _ := xr.Resource.GetString("spec.dueOrderTime")
	dueOrderTime, _ := strconv.Atoi(dueOrderTimeString)
	currentTimestamp := int(time.Now().Unix())
	fmt.Println("DueOrderTime: ", dueOrderTime, "CurrentTimestamp: ", currentTimestamp)
	if currentTimestamp >= dueOrderTime || len(Voters) == len(realUsers) {

		jobTemplate := map[string]interface{}{}

		unstructuredData := composed.Unstructured{}
		unstructuredDataByte, err := json.Marshal(jobTemplate)
		if err != nil {
			fmt.Println(err)
		}
		err = json.Unmarshal(unstructuredDataByte, &unstructuredData)
		if err != nil {
			fmt.Println(err)
		}
		desired, err := request.GetDesiredComposedResources(req)

		if err != nil {
			return rsp, err
		}
		desired[resource.Name(jobTemplate["metadata"].(map[string]interface{})["name"].(string))] = &resource.DesiredComposed{Resource: &unstructuredData}

		if err := response.SetDesiredComposedResources(rsp, desired); err != nil {
			return rsp, err
		}

		fmt.Println("DueOrderTime is passed due, send results in slack chanel")
	} else {
		if timeElapsed(xr, rsp) {
			sendSlackMessage(xr, api, input.SlackChanelID, input.SlackNotifyMessage)
		}

		ingressTemplate := map[string]interface{}{
			"apiVersion": "kubernetes.crossplane.io/v1alpha1",
			"kind":       "Object",
			"metadata": map[string]interface{}{
				"name": input.IngressName,
			},
			"spec": map[string]interface{}{
				"forProvider": map[string]interface{}{
					"manifest": map[string]interface{}{
						"apiVersion": "networking.k8s.io/v1",
						"kind":       "Ingress",
						"metadata": map[string]interface{}{
							"name":      input.IngressName,
							"namespace": "default",
						},
						"spec": map[string]interface{}{
							"rules": []map[string]interface{}{
								{
									"host": input.IngressHostName,
									"http": map[string]interface{}{
										"paths": []map[string]interface{}{
											{
												"pathType": "Prefix",
												"path":     "/",
												"backend": map[string]interface{}{
													"service": map[string]interface{}{
														"name": input.PollServiceName,
														"port": map[string]interface{}{
															"number": input.PollServicePort,
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
				"providerConfigRef": map[string]interface{}{"name": input.ProviderConfigRef},
			},
		}

		serviceTemplate := map[string]interface{}{
			"apiVersion": "kubernetes.crossplane.io/v1alpha1",
			"kind":       "Object",
			"metadata": map[string]interface{}{
				"name": input.PollServiceName,
			},
			"spec": map[string]interface{}{
				"forProvider": map[string]interface{}{
					"manifest": map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Service",
						"metadata": map[string]interface{}{
							"name":      input.PollServiceName,
							"namespace": "default",
						},
						"spec": map[string]interface{}{
							"selector": map[string]interface{}{
								"app": "poll",
							},
							"ports": []map[string]interface{}{
								{
									"protocol":   "TCP",
									"port":       input.PollServicePort,
									"targetPort": input.PollServiceTargetPort,
								},
							},
							"type": "ClusterIP",
						},
					},
				},
				"managementPolicy":  "Default",
				"providerConfigRef": map[string]interface{}{"name": input.ProviderConfigRef},
			},
		}

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
		desired, err := request.GetDesiredComposedResources(req)

		if err != nil {
			return rsp, err
		}
		// List of templates
		templates := []map[string]interface{}{deploymentTemplate, ingressTemplate, serviceTemplate}

		// Process each template
		for _, template := range templates {
			unstructuredData := composed.Unstructured{}
			unstructuredDataByte, err := json.Marshal(template)
			if err != nil {
				return rsp, err
			}
			err = json.Unmarshal(unstructuredDataByte, &unstructuredData)
			if err != nil {
				fmt.Println(err)
			}
			desired[resource.Name(template["metadata"].(map[string]interface{})["name"].(string))] = &resource.DesiredComposed{Resource: &unstructuredData}
		}

		if err := response.SetDesiredComposedResources(rsp, desired); err != nil {
			return rsp, err
		}

	}
	return rsp, nil
}
