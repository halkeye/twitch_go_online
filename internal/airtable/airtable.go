package airtable

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

type webhookCallback func(usernames []string)

type Airtable struct {
	APIKey    string
	BaseID    string
	TableName string
}

func New(APIKey string, baseID string, tableName string) *Airtable {
	return &Airtable{
		APIKey:    APIKey,
		BaseID:    baseID,
		TableName: tableName,
	}
}

func (at *Airtable) HttpHandler(callback webhookCallback) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the request body.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			panic(errors.Wrap(err, "Error reading incoming post"))
		}
		defer r.Body.Close()

		log.Infof("got webookhook from airtable: %s", string(body))
		// there's a fancy payload which you call https://airtable.com/developers/web/api/model/webhooks-payload but really we just want usernames again
		usernames, err := at.Usernames()
		if err != nil {
			panic(errors.Wrap(err, "getting usernames after webhook"))
		}
		callback(usernames)
	})
}

func (at *Airtable) Usernames() ([]string, error) {
	usernames := []string{}

	endpoint := fmt.Sprintf("https://api.airtable.com/v0/%s/%s", at.BaseID, at.TableName)

	var result map[string]interface{}
	err := at.get(endpoint, &result)
	if err != nil {
		return usernames, errors.Wrap(err, "unable to fetch rows")
	}

	for _, record := range result["records"].([]interface{}) {
		fields := record.(map[string]interface{})["fields"].(map[string]interface{})
		if len(fields) == 0 {
			continue
		}
		log.WithField("fields", mustJson(fields)).Debug("fields")
		if twitchAccountField, ok := fields["Twitch Account"]; ok {
			log.WithField("twitchAccountField", twitchAccountField).Debug("twitchAccountField")
			if val, ok := twitchAccountField.(string); ok {
				val = strings.TrimSpace(val)
				if len(val) > 0 {
					usernames = append(usernames, val)
				}
			} else {
				log.Warn("record has no twitch account")
			}
		} else {
			return usernames, errors.New("record has no twitch account")
		}
	}

	return usernames, nil
}

func (at *Airtable) RegisterWebhook(webhookURL string) error {
	id, err := at.findMatchingWebhooks(webhookURL)
	if err != nil {
		return err
	}

	if len(id) != 0 {
		return at.refreshWebhook(id)
	}

	var result map[string]interface{}
	err = at.post(fmt.Sprintf("https://api.airtable.com/v0/bases/%s/webhooks", at.BaseID), map[string]interface{}{
		"notificationUrl": webhookURL,
		"specification": map[string]interface{}{
			"options": map[string]interface{}{
				"filters": map[string]interface{}{
					"dataTypes": []string{"tableData"},
				},
			},
		},
	}, &result)
	if err != nil {
		return errors.Wrap(err, "unable to create webhook")
	}

	at.refreshWebhook(result["id"].(string))

	return nil
}

func (at *Airtable) refreshWebhook(id string) error {
	var result map[string]interface{}

	endpoint := fmt.Sprintf("https://api.airtable.com/v0/bases/%s/webhooks/%s/refresh", at.BaseID, id)

	err := at.post(endpoint, nil, &result)
	if err != nil {
		return errors.Wrap(err, "unable to refresh webhook")
	}

	parsed, err := time.Parse(time.RFC3339, result["expirationTime"].(string))
	if err != nil {
		return errors.Wrap(err, "unable to parse time")
	}

	expiresInDur := parsed.Sub(time.Now())
	log.Infof("Scheduling refreshing airtable webhook %s in %d seconds at %s", id, expiresInDur, parsed.String())

	time.AfterFunc(expiresInDur, func() {
		log.Infof("Refreshing airtable webook %s", id)
		at.refreshWebhook(id)
	})

	return nil
}

func (at *Airtable) findMatchingWebhooks(URL string) (string, error) {
	endpoint := fmt.Sprintf("https://api.airtable.com/v0/bases/%s/webhooks", at.BaseID)

	var result map[string]interface{}
	err := at.get(endpoint, &result)
	if err != nil {
		return "", errors.Wrap(err, "unable to decode json")
	}

	for _, record := range result["webhooks"].([]interface{}) {
		webhook := record.(map[string]interface{})
		if webhook["isHookEnabled"].(bool) && webhook["notificationUrl"] == URL {
			return webhook["id"].(string), nil
		}
	}
	return "", nil
}

func decodeJSON(r io.Reader, v interface{}) error {
	return json.NewDecoder(r).Decode(v)
}

func (at *Airtable) get(endpoint string, result interface{}) error {
	var err error

	log.WithField("endpoint", endpoint).Debug("getting endpoint")
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return errors.Wrap(err, "unable to get from airtable")
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", at.APIKey))
	req.Header.Set("Content-Type", "application/json")

	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return errors.Wrap(err, "unable map request")
	}
	defer resp.Body.Close()

	if result != nil {
		if err := decodeJSON(resp.Body, &result); err != nil {
			return errors.Wrap(err, "unable to decode request")
		}
	}

	return nil
}

func (at *Airtable) post(endpoint string, body interface{}, result interface{}) error {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return errors.Wrap(err, "unable to create payload")
		}
	}

	log.WithField("endpoint", endpoint).Debug("posting endpoint")
	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return errors.Wrap(err, "unable to create request")
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", at.APIKey))
	req.Header.Set("Content-Type", "application/json")

	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return errors.Wrap(err, "unable to make request")
	}
	defer resp.Body.Close()

	if result != nil {
		if err := decodeJSON(resp.Body, &result); err != nil {
			return errors.Wrap(err, "unable to decode request")
		}
	}

	return nil
}

func mustJson(data interface{}) string {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}
