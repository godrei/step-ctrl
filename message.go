package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/bitrise-io/go-utils/log"
)

// Message to post to a slack channel.
// See also: https://api.slack.com/methods/chat.postMessage
type Message struct {
	// Channel to send message to.
	//
	// Can be an encoded ID (eg. C024BE91L), or the channel's name (eg. #general).
	Channel string `json:"channel"`

	// Text of the message to send. Required, unless providing only attachments instead.
	Text string `json:"text,omitempty"`

	// IconEmoji is the emoji to use as the icon for the message. Overrides IconUrl.
	IconEmoji string `json:"icon_emoji,omitempty"`

	// IconURL is the URL to an image to use as the icon for the message.
	IconURL string `json:"icon_url,omitempty"`

	// LinkNames linkifies channel names and usernames.
	LinkNames bool `json:"link_names,omitempty"`

	// Username specifies the bot's username for the message.
	Username string `json:"username,omitempty"`
}

// postMessage sends a message to a channel.
func postMessage(msg Message, apiToken, webhookURL string) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	log.Debugf("Request to Slack: %s\n", b)

	url := strings.TrimSpace(webhookURL)
	if url == "" {
		url = "https://slack.com/api/chat.postMessage"
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Add("Content-Type", "application/json; charset=utf-8")

	if string(apiToken) != "" {
		req.Header.Add("Authorization", "Bearer "+string(apiToken))
	}

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send the request: %s", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); err == nil {
			err = cerr
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("server error: %s, failed to read response: %s", resp.Status, err)
		}
		return fmt.Errorf("server error: %s, response: %s", resp.Status, body)
	}

	return nil
}
