package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/nlopes/slack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client/config"
)

var (
	token              = os.Getenv("SLACK_API_TOKEN")
	channelID          = os.Getenv("SLACK_CHANEL_ID")
	pollName           = os.Getenv("POLL_NAME")
	pollTitle          = os.Getenv("POLL_TITLE")
	slackNotifyMessage = os.Getenv("SLACK_NOTIFY_MESSAGE")
)

// Voter represents the structure of an Voter reference.
type Voter struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Message represents the structure of a message.
type Message struct {
	Question string `json:"question"`
	Response string `json:"response"`
	Result   string `json:"result"`
}

// Poll represents the structure of a poll.
type Poll struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		DeliveryTime int64   `json:"deliveryTime"`
		DueOrderTime int64   `json:"dueOrderTime"`
		DueTakeTime  int64   `json:"dueTakeTime"`
		Schedule     string  `json:"schedule"`
		Voters       []Voter `json:"voters"`
		Title        string  `json:"title"`
		Messages     Message `json:"messages"`
	} `json:"spec"`
	Status struct {
		Done                 bool  `json:"done"`
		LastNotificationTime int64 `json:"lastNotificationTime"`
	} `json:"status"`
}

// getK8sResource gets the Kubernetes resource.
func getK8sResource(dynamicClient dynamic.Interface, ctx context.Context, pollSlackName string, resId schema.GroupVersionResource) (*Poll, error) {

	res, err := dynamicClient.Resource(resId).Namespace("").
		List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, item := range res.Items {
		res := &Poll{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, res); err != nil {
			return nil, fmt.Errorf("error converting Unstructured to Poll struct: %v", err)
		}
		if res.GetObjectMeta().GetName() == pollSlackName {
			return res, nil
		}
	}
	return nil, fmt.Errorf("poll resource with name %s not found", pollSlackName)
}

func main() {
	config, err := ctrl.GetConfig()
	if err != nil {
		fmt.Println("error getting config", err)
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		fmt.Println("error getting client", err)
	}
	resourceId := schema.GroupVersionResource{
		Group:    "kndp.io",
		Version:  "v1alpha1",
		Resource: "polls",
	}

	pollResource, _ := getK8sResource(client, context.Background(), pollName, resourceId)
	pollResource.GetObjectMeta().SetManagedFields(nil)
	pollBytes, _ := json.Marshal(pollResource)
	_, err = client.Resource(resourceId).Namespace("").Patch(context.Background(), pollResource.GetObjectMeta().GetName(), types.MergePatchType, pollBytes, metav1.PatchOptions{FieldManager: "slack-collector"})
	if err != nil {
		fmt.Println("Error patching poll resource", err)
	}

	statusBytes, _ := json.Marshal(map[string]interface{}{
		"status": map[string]interface{}{
			"done":                 false,
			"lastNotificationTime": time.Now().Unix(),
		},
	})

	// Use the "/status" subresource to update just the status
	_, err = client.Resource(resourceId).Namespace("").Patch(
		context.Background(),
		pollResource.GetObjectMeta().GetName(),
		types.MergePatchType,
		statusBytes,
		metav1.PatchOptions{FieldManager: "slack-collector"},
		"/status",
	)
	if err != nil {
		fmt.Println("Error patching poll status", err)
	}

	api := slack.New(token)

	members, _, err := api.GetUsersInConversation(&slack.GetUsersInConversationParameters{
		ChannelID: channelID,
	})
	if err != nil {
		fmt.Println("error getting users in conversation", err)
	}

	for _, memberID := range members {
		userInfo, err := api.GetUserInfo(memberID)
		if err != nil {
			fmt.Println("error getting user info for", memberID, err)
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
		}

		channelID, _, err := api.PostMessage(
			userInfo.ID,
			slack.MsgOptionText("", true),
			slack.MsgOptionAttachments(attachment),
			slack.MsgOptionAsUser(true),
		)
		if err != nil {
			fmt.Println("error sending message to user in channel: ", userInfo.Name, channelID, err)
		} else {
			fmt.Println("message sent to user in channel: ", userInfo.Name, channelID)
		}

	}
}
