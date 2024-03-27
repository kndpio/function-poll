// Package slackchannel provides functions for interacting with Slack channels and sending messages to users.
package slackchannel

import (
	"strconv"
	"strings"

	"github.com/slack-go/slack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/crossplane/function-sdk-go/logging"
	"github.com/crossplane/function-sdk-go/resource"
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

// UserVoted checks if the user should receive a message based on status
func UserVoted(voters []Voter, userName string) bool {
	for _, Voter := range voters {
		if Voter.Name == userName {
			return Voter.Status == ""
		}
	}
	return true
}

// ProcessSlackMembers gets and process slack members
func ProcessSlackMembers(api *slack.Client, channelID string, logger logging.Logger) ([]string, error) {
	members, _, err := api.GetUsersInConversation(&slack.GetUsersInConversationParameters{ChannelID: channelID})
	if err != nil {
		return nil, err
	}
	realUsers := make([]string, 0)
	for _, memberID := range members {
		userInfo, err := api.GetUserInfo(memberID)
		if err != nil {
			logger.Info("error getting user info for", memberID, err)
			continue
		}

		if !userInfo.IsBot {
			realUsers = append(realUsers, userInfo.Name)
		}
	}
	return realUsers, nil
}

// SendSlackMessage is for sending messages to users in slack
func SendSlackMessage(xr *resource.Composite, api *slack.Client, channelID string, slackNotifyMessage string, logger logging.Logger) {
	members, _, err := api.GetUsersInConversation(&slack.GetUsersInConversationParameters{
		ChannelID: channelID,
	})
	if err != nil {
		logger.Info("error getting conversation members", "warning", err)
	}

	logger.Debug("conversation members:", members)
	pollName, _ := xr.Resource.GetString("metadata.name")
	pollTitle, _ := xr.Resource.GetString("spec.title")
	poll := Poll{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(xr.Resource.Object, &poll); err != nil {
		logger.Info("error converting Unstructured to Poll", "warning", err)
	}

	for _, memberID := range members {
		userInfo, err := api.GetUserInfo(memberID)
		if err != nil {
			logger.Info("error getting user info for", memberID, err)
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

		if UserVoted(poll.Spec.Voters, userInfo.Name) {
			channelID, _, err := api.PostMessage(
				userInfo.ID,
				slack.MsgOptionText("", true),
				slack.MsgOptionAttachments(attachment),
				slack.MsgOptionAsUser(true),
			)
			if err != nil {
				logger.Info("error sending message to user: ", userInfo.Name, userInfo.ID, err)
			} else {
				logger.Debug("message sent to user in channel: \n", userInfo.Name, userInfo.ID, channelID)
			}

		}
	}
}

func countUsers(voters []Voter) int {
	count := 0
	for _, voter := range voters {
		if strings.EqualFold(voter.Status, "yes") {
			count++
		}
	}
	return count
}

// SlackOrder sends an order notification via Slack.
func SlackOrder(input *v1beta1.Input, api *slack.Client, xr *resource.Composite, logger logging.Logger) {
	pollTitle, _ := xr.Resource.GetString("spec.title")

	poll := Poll{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(xr.Resource.Object, &poll); err != nil {
		logger.Info("error converting Unstructured to Poll:", err)
	}
	textContent := countUsers(poll.Spec.Voters)

	attachment := slack.Attachment{
		Color:      "#f9a41b",
		CallbackID: "meal",
		Title:      pollTitle,
		TitleLink:  pollTitle,
		Text:       "Total votes: " + strconv.Itoa(textContent),
		MarkdownIn: []string{},
	}

	channelID, timestamp, err := api.PostMessage(
		input.SlackChanelID,
		slack.MsgOptionText("", false),
		slack.MsgOptionAttachments(attachment),
		slack.MsgOptionAsUser(true),
	)
	if err != nil {
		logger.Info("error sending slack message", "warning", err)
	} else {
		logger.Info("message successfully sent to channel", channelID, timestamp)
	}

}
