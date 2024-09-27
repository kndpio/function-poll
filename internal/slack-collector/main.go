package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/slack-go/slack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
)

// SelectedOptionValue represents the structure of the selected option value from Slack.
type SelectedOptionValue struct {
	User struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"user"`
	CallbackID string `json:"callback_id"`
	Actions    []struct {
		Name            string `json:"name"`
		Type            string `json:"type"`
		SelectedOptions []struct {
			Value string `json:"value"`
		} `json:"selected_options"`
	} `json:"actions"`
	OriginalMessage struct {
		Attachments []struct {
			Title string `json:"title"`
		} `json:"attachments"`
	} `json:"original_message"`
}

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

var (
	api      = slack.New(os.Getenv("SLACK_API_TOKEN"))
	path     = os.Getenv("SLACK_COLLECTOR_PATH")
	port     = os.Getenv("SLACK_COLLECTOR_PORT")
	color    = os.Getenv("COLOR")
	response string
)

// handleEventsEndpoint handles the events endpoint.
func handleEventsEndpoint(w http.ResponseWriter, r *http.Request, dynamicClient dynamic.Interface, ctx context.Context) {
	payload, err := url.QueryUnescape(r.FormValue("payload"))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Println("Error decoding payload:", err)
		return
	}
	var data SelectedOptionValue
	err = json.Unmarshal([]byte(payload), &data)
	if err != nil {
		fmt.Println("Error decoding JSON:", err)
		return
	}
	selectedOption := data.Actions[0].SelectedOptions[0].Value
	pollSlackName := data.CallbackID

	user := data.User.Name
	userID := data.User.ID

	err = patchVoterStatus(user, pollSlackName, selectedOption, dynamicClient, ctx)
	if err != nil {
		fmt.Println("Error patching Voter status:", err)
	}
	respondMsg(userID, user, selectedOption, pollSlackName)
}

// patchVoterStatus patches the employee reference status.
func patchVoterStatus(user, pollSlackName, selectedOption string, dynamicClient dynamic.Interface, ctx context.Context) error {
	resourceId := schema.GroupVersionResource{
		Group:    "kndp.io",
		Version:  "v1alpha1",
		Resource: "polls",
	}
	pollResource, err := getK8sResource(dynamicClient, ctx, pollSlackName, resourceId)
	if err != nil {
		return err
	}
	response = pollResource.Spec.Messages.Response
	foundUser := false
	for i := range pollResource.Status.Voters {
		if pollResource.Status.Voters[i].Name == user {
			pollResource.Status.Voters[i].Status = selectedOption
			foundUser = true
			break
		}
	}

	if !foundUser {
		newVoter := Voter{
			Name:   user,
			Status: selectedOption,
		}
		pollResource.Status.Voters = append(pollResource.Status.Voters, newVoter)
	}

	pollResource.GetObjectMeta().SetManagedFields(nil)

	statusBytes, _ := json.Marshal(map[string]interface{}{
		"status": map[string]interface{}{
			"voters": pollResource.Status.Voters,
		},
	})

	_, err = dynamicClient.Resource(resourceId).Namespace("").Patch(
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

	return nil
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

// respondMsg sends a response message to Slack.
func respondMsg(userID string, userName string, selectedOption string, pollName string) {

	attachment := slack.Attachment{
		Color:      color,
		CallbackID: pollName,
		Text:       response + "\n Selected: " + selectedOption,
		Fields:     []slack.AttachmentField{},
		Actions:    []slack.AttachmentAction{},
		MarkdownIn: []string{},
		Blocks:     slack.Blocks{},
	}
	channelID, _, err := api.PostMessage(
		userID,
		slack.MsgOptionText("", true),
		slack.MsgOptionAttachments(attachment),
		slack.MsgOptionAsUser(true),
	)
	if err != nil {
		fmt.Printf("error: %s\n", err)
		return
	}

	fmt.Printf("message sent to user %s (%s) in channel %s\n", userName, userID, channelID)
}

func main() {
	ctx := context.Background()
	config := ctrl.GetConfigOrDie()
	dynamicClient := dynamic.NewForConfigOrDie(config)
	if port == "" {
		port = "3000"
	}
	if path == "" {
		path = "/events"
	}
	http.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		handleEventsEndpoint(w, r, dynamicClient, ctx)
	})

	fmt.Println("[INFO] Server listening on port:", port)
	http.ListenAndServe(":"+port, nil)
}
