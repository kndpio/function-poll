// Package slackchannel provides functions for interacting with Slack channels and sending messages to users.
package slackchannel

import (
	"strings"

	"github.com/slack-go/slack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/crossplane/function-sdk-go/logging"
	"github.com/crossplane/function-sdk-go/resource"
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
	Color    string `json:"color"`
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
		Title        string  `json:"title"`
		Messages     Message `json:"messages"`
	} `json:"spec"`
	Status struct {
		Voters               []Voter `json:"voters"`
		LastNotificationTime int64   `json:"lastNotificationTime"`
	} `json:"status"`
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

// PatchVoters patch status.voters key with an empty array.
func PatchVoters(xr *resource.Composite, logger logging.Logger) (*resource.Composite, int) {
	poll := Poll{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(xr.Resource.Object, &poll); err != nil {
		logger.Info("error converting Unstructured to Poll:", err)
	}
	voters := countUsers(poll.Status.Voters)
	poll.Status.Voters = []Voter{}
	xr.Resource.Object, _ = runtime.DefaultUnstructuredConverter.ToUnstructured(&poll)
	return xr, voters
}
