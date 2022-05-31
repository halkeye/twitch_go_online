package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"text/template"
	"time"

	helix "github.com/nicklaw5/helix/v2"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

const (
	postMessageTmpl = `Look alive, mateys! {{.ChannelName}} is playing {{.Game}}
Channel URL: {{.ChannelUrl}}

Go give them some love!`
)

// eventSubNotification is a struct to hold the eventSub webhook request from Twitch.
type eventSubNotification struct {
	Challenge    string                     `json:"challenge"`
	Event        json.RawMessage            `json:"event"`
	Subscription helix.EventSubSubscription `json:"subscription"`
}

func fetchStreamInfo(client *helix.Client, user_id string) (*helix.Stream, error) {
	streams, err := client.GetStreams(&helix.StreamsParams{UserIDs: []string{user_id}})
	if err != nil {
		return nil, err
	}
	if streams.ErrorStatus != 0 {
		return nil, fmt.Errorf("error fetching stream info status=%d %s error=%s", streams.ErrorStatus, streams.Error, streams.ErrorMessage)
	}

	if len(streams.Data.Streams) > 0 {
		return &streams.Data.Streams[0], nil
	}

	return nil, fmt.Errorf("no stream returned for uid: %s", user_id)
}

func handlerEventSub(secretKey string, client *helix.Client, tmpl *template.Template) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the request body.
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			log.Println(err)
			return
		}
		defer r.Body.Close()

		// Verify that the notification came from twitch using the secret.
		if !helix.VerifyEventSubNotification(secretKey, r.Header, string(body)) {
			log.Println("invalid signature on message")
			return
		} else {
			log.Println("verified signature on message")
		}

		log.Printf("%s\n", body)

		// Read the request into eventSubNotification struct.

		var vals eventSubNotification
		err = json.NewDecoder(bytes.NewReader(body)).Decode(&vals)
		if err != nil {
			log.Println(err)
			return
		}

		// If there's a challenge in the request respond with only the challenge to verify the eventsubscription.
		if vals.Challenge != "" {
			w.Write([]byte(vals.Challenge))
			return
		}

		if vals.Subscription.Type == "stream.online" {
			var onlineEvent helix.EventSubStreamOnlineEvent
			_ = json.NewDecoder(bytes.NewReader(vals.Event)).Decode(&onlineEvent)
			log.Printf("got online event for: %s\n", onlineEvent.BroadcasterUserName)
			w.WriteHeader(200)
			w.Write([]byte("ok"))

			stream, err := fetchStreamInfo(client, onlineEvent.BroadcasterUserID)
			if err != nil {
				log.Error(errors.Wrap(err, fmt.Sprintf("Error fetching stream info for %s (uid: %s)", onlineEvent.BroadcasterUserName, onlineEvent.BroadcasterUserID)))
				return
			}

			tmpl.Execute(os.Stderr, map[string]string{
				"Game":        stream.GameName,
				"ChannelName": onlineEvent.BroadcasterUserName,
				"ChannelUrl":  fmt.Sprintf("https://www.twitch.tv/%s", onlineEvent.BroadcasterUserName),
			})
		} else {
			log.Errorf("error: event type %s has not been implemented -- pull requests welcome!", r.Header.Get("Twitch-Eventsub-Subscription-Type"))
		}
	})
}

func withLogging(h http.Handler) http.Handler {
	logFn := func(rw http.ResponseWriter, r *http.Request) {
		start := time.Now()

		uri := r.RequestURI
		method := r.Method
		h.ServeHTTP(rw, r) // serve the original request

		duration := time.Since(start)

		// log request details
		log.WithFields(log.Fields{
			"uri":      uri,
			"method":   method,
			"duration": duration,
		})
	}
	return http.HandlerFunc(logFn)
}

func main() {
	tmpl := template.Must(template.New("message").Parse(postMessageTmpl))
	if tmpl == nil {
		panic("ahh")
	}

	clientId := os.Getenv("TWITCH_CLIENT_ID")
	clientSecret := os.Getenv("TWITCH_CLIENT_SECRET")
	secretKey := os.Getenv("SECRETKEY")

	if len(secretKey) == 0 {
		panic("no secret key provided")
	}

	client, err := helix.NewClient(&helix.Options{
		ClientID:     clientId,
		ClientSecret: clientSecret,
	})
	if err != nil {
		panic(err)
	}

	resp, err := client.RequestAppAccessToken([]string{"user:read:email"})
	if err != nil {
		// handle error
	}

	// Set the access token on the client
	client.SetAppAccessToken(resp.Data.AccessToken)

	port := ":3000"
	if os.Getenv("PORT") != "" {
		port = ":" + os.Getenv("PORT")
	}

	err = registerSubscription(secretKey, client, []string{"halkeye"})
	if err != nil {
		log.Error(err)
		return
	}

	/*
		ticker := time.NewTicker(5 * time.Second)
		quit := make(chan struct{})
		go func() {
			for {
				select {
				case <-ticker.C:
					getSubResp, err := client.GetEventSubSubscriptions(&helix.EventSubSubscriptionsParams{})
					if err != nil {
						continue
					}
					log.Info(mustJson(getSubResp.Data.EventSubSubscriptions))
				case <-quit:
					ticker.Stop()
					return
				}
			}
		}()
	*/

	//webhook/callbacks//webhook/callbacks Listen and serve.
	log.Printf("server starting on %s\n", port)
	http.Handle("/webhook/callbacks", withLogging(handlerEventSub(secretKey, client, tmpl)))
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "\n")
	})
	log.Fatal(http.ListenAndServe(port, nil))
}

func registerSubscription(secretKey string, client *helix.Client, usernames []string /* url prefix*/) error {
	/*
	* 1) Lookup all usernames and get IDs
	* 2) Delete all subscriptions with url prefix
	* 3) Register all userids
	 */

	getUserResp, err := client.GetUsers(&helix.UsersParams{Logins: usernames})
	if err != nil {
		return errors.Wrap(err, "Error getting subscriptions")
	}

	userIds := []string{}

	for _, userData := range getUserResp.Data.Users {
		userIds = append(userIds, userData.ID)
	}

	log.Info(mustJson(getUserResp.Data))

	getSubResp, err := client.GetEventSubSubscriptions(&helix.EventSubSubscriptionsParams{})
	if err != nil {
		return errors.Wrap(err, "Error getting subscriptions")
	}

	log.Info(mustJson(getSubResp.Data))

	for _, sub := range getSubResp.Data.EventSubSubscriptions {
		_, err = client.RemoveEventSubSubscription(sub.ID)
		if err != nil {
			return errors.Wrap(err, "Error removing subscriptions")
		}
	}

	createSubResp, err := client.CreateEventSubSubscription(&helix.EventSubSubscription{
		Type:      helix.EventSubTypeStreamOnline,
		Version:   "1",
		Condition: helix.EventSubCondition{BroadcasterUserID: userIds[0]},
		Transport: helix.EventSubTransport{
			Method:   "webhook",
			Callback: "https://dev.g4v.dev/webhook/callbacks",
			Secret:   secretKey,
		},
	})

	if err != nil {
		return errors.Wrap(err, "Error creating subscription")
	}

	if createSubResp.ErrorStatus > 0 {
		return errors.Errorf("Error creating subscription (%d) - %s", createSubResp.ErrorStatus, createSubResp.Error)
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
