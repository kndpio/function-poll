// Package slackchannel provides functions for interacting with Slack channels and sending messages to users.
package slackchannel

import (
	"os"
	"strconv"
	"strings"

	"github.com/slack-go/slack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/crossplane/function-sdk-go/logging"
	"github.com/crossplane/function-sdk-go/resource"
	"github.com/crossplane/function-template-go/input/v1beta1"
)

var (
	channelId = os.Getenv("SLACK_CHANEL_ID")
	pollTitle string
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
func SlackOrder(input *v1beta1.Input, api *slack.Client, xr *resource.Composite, logger logging.Logger, resultText string) *resource.Composite {
	pollTitle, _ = xr.Resource.GetString("spec.title")

	poll := Poll{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(xr.Resource.Object, &poll); err != nil {
		logger.Info("error converting Unstructured to Poll:", err)
	}
	textContent := countUsers(poll.Spec.Voters)

	attachment := slack.Attachment{
		Color:      "#f9a41b",
		CallbackID: pollTitle,
		Title:      pollTitle,
		TitleLink:  pollTitle,
		Text:       resultText + strconv.Itoa(textContent),
		MarkdownIn: []string{},
	}

	channelID, timestamp, err := api.PostMessage(
		channelId,
		slack.MsgOptionText("", false),
		slack.MsgOptionAttachments(attachment),
		slack.MsgOptionAsUser(true),
	)

	if err != nil {
		logger.Info("error sending slack message", "warning", err)
	} else {
		logger.Info("message successfully sent to channel", channelID, timestamp)
	}
	poll.Spec.Voters = nil
	xr.Resource.Object, err = runtime.DefaultUnstructuredConverter.ToUnstructured(&poll)
	if err != nil {
		logger.Info("error converting Poll to Unstructured:", err)
	}
	return xr
}
