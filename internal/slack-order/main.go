package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/slack-go/slack"
)

var (
	token            = os.Getenv("SLACK_API_TOKEN")
	resultsChannelID = os.Getenv("SLACK_RESULTS_CHANEL_ID")
	votersDetails    = os.Getenv("POLL_VOTERS_DETAILS")
	pollName         = os.Getenv("POLL_NAME")
	pollTitle        = os.Getenv("POLL_TITLE")
	resultText       = os.Getenv("RESULT_TEXT")
	results          = os.Getenv("RESULTS")
	color            = os.Getenv("COLOR")
)

// Voter represents the structure of a voter.
type Voter struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// SlackOrder sends an order notification via Slack.
func SlackOrder(api *slack.Client) {
	var voters []Voter
	if err := json.Unmarshal([]byte(votersDetails), &voters); err != nil {
		fmt.Println("Error parsing voters details:", err)
		return
	}
	var votersFormatted []string
	for _, voter := range voters {
		votersFormatted = append(votersFormatted, fmt.Sprintf("%s: %s", voter.Name, voter.Status))
	}
	votersList := strings.Join(votersFormatted, "\n")
	completeText := fmt.Sprintf(
		"*%s*\nApproved: *%s*\n\n*Voter Details:*\n%s",
		resultText,
		results,
		votersList,
	)

	attachment := slack.Attachment{
		Color:      color,
		CallbackID: pollTitle,
		Title:      pollTitle,
		Text:       completeText,
		MarkdownIn: []string{"text"},
	}

	channelID, timestamp, err := api.PostMessage(
		resultsChannelID,
		slack.MsgOptionText("", false),
		slack.MsgOptionAttachments(attachment),
		slack.MsgOptionAsUser(true),
	)

	if err != nil {
		fmt.Println("Error sending slack message:", err)
	} else {
		fmt.Println("Message successfully sent to channel", channelID, "at", timestamp)
	}
}

func main() {
	api := slack.New(token)
	SlackOrder(api)
}
