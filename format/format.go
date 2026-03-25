// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package format

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

func ThreadData(data *mmapi.ThreadData) string {
	result := ""
	for _, post := range data.Posts {
		result += fmt.Sprintf("%s: %s\n\n", data.UsersByID[post.UserId].Username, PostBody(post))
	}

	return result
}

func PostBody(post *model.Post) string {
	attachments := post.Attachments()
	if len(attachments) > 0 {
		result := strings.Builder{}
		result.WriteString(post.Message)
		for _, attachment := range attachments {
			result.WriteString("\n")
			if attachment.Pretext != "" {
				result.WriteString(attachment.Pretext)
				result.WriteString("\n")
			}
			if attachment.Title != "" {
				result.WriteString(attachment.Title)
				result.WriteString("\n")
			}
			if attachment.Text != "" {
				result.WriteString(attachment.Text)
				result.WriteString("\n")
			}
			for _, field := range attachment.Fields {
				value, err := json.Marshal(field.Value)
				if err != nil {
					continue
				}
				result.WriteString(field.Title)
				result.WriteString(": ")
				result.Write(value)
				result.WriteString("\n")
			}

			if attachment.Footer != "" {
				result.WriteString(attachment.Footer)
				result.WriteString("\n")
			}
		}
		return result.String()
	}
	return post.Message
}
