package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/halkeye/twitch_go_online/internal/airtable"
	"github.com/halkeye/twitch_go_online/internal/discordsender"

	sentry "github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
	helix "github.com/nicklaw5/helix/v2"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
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

func handlerEventSub(secretKey string, client *helix.Client, ds *discordsender.DiscordSender) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the request body.
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			panic(errors.Wrap(err, "Error reading incoming post"))
		}
		defer r.Body.Close()

		// Verify that the notification came from twitch using the secret.
		if !helix.VerifyEventSubNotification(secretKey, r.Header, string(body)) {
			log.Println("invalid signature on message")
			return
		} else {
			log.Println("verified signature on message")
		}

		log.Printf("Body: %s\n", body)

		// Read the request into eventSubNotification struct.

		var vals eventSubNotification
		err = json.NewDecoder(bytes.NewReader(body)).Decode(&vals)
		if err != nil {
			panic(err)
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

			// We got the event successfully, let twitch know
			w.WriteHeader(200)
			w.Write([]byte("ok"))

			stream, err := fetchStreamInfo(client, onlineEvent.BroadcasterUserID)
			if err != nil {
				log.Error(err)
				panic(errors.Wrap(err, fmt.Sprintf("Error fetching stream info for %s (uid: %s)", onlineEvent.BroadcasterUserName, onlineEvent.BroadcasterUserID)))
			}

			tmplParams := map[string]string{
				"Game":        escapeMarkdown(stream.GameName),
				"ChannelName": escapeMarkdown(stream.UserName),
				"ChannelUrl":  fmt.Sprintf("https://www.twitch.tv/%s", stream.UserLogin),
			}
			if err := ds.Send(tmplParams); err != nil {
				panic(errors.Wrap(err, "unable to send webhook"))
			}
		} else {
			log.Errorf("error: event type %s has not been implemented -- pull requests welcome!", r.Header.Get("Twitch-Eventsub-Subscription-Type"))
		}
	})
}

//func withLogging(h http.Handler) http.Handler {
//  logFn := func(rw http.ResponseWriter, r *http.Request) {
//    start := time.Now()

//    uri := r.RequestURI
//    method := r.Method
//    h.ServeHTTP(rw, r) // serve the original request

//    duration := time.Since(start)

//    // log request details
//    log.WithFields(log.Fields{
//      "uri":      uri,
//      "method":   method,
//      "duration": duration,
//    })
//  }
//  return http.HandlerFunc(logFn)
//}

func registerSubscription(secretKey string, client *helix.Client, usernames []string, publicUrl string) error {
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
		log.Infof("Monitoring: %s => %s", userData.Login, userData.ID)
	}

	getSubResp, err := client.GetEventSubSubscriptions(&helix.EventSubSubscriptionsParams{})
	if err != nil {
		return errors.Wrap(err, "Error getting subscriptions")
	}

	for _, sub := range getSubResp.Data.EventSubSubscriptions {
		if strings.HasPrefix(sub.Transport.Callback, publicUrl) {
			_, err = client.RemoveEventSubSubscription(sub.ID)
			if err != nil {
				return errors.Wrap(err, "Error removing subscriptions")
			}
		} else {
			log.Infof("Not one of my subscriptions: %s => %s", sub.Transport.Callback, sub.Condition.BroadcasterUserID)
		}
	}

	for _, userId := range userIds {
		createSubResp, err := client.CreateEventSubSubscription(&helix.EventSubSubscription{
			Type:      helix.EventSubTypeStreamOnline,
			Version:   "1",
			Condition: helix.EventSubCondition{BroadcasterUserID: userId},
			Transport: helix.EventSubTransport{
				Method:   "webhook",
				Callback: fmt.Sprintf("%swebhook/callbacks", publicUrl),
				Secret:   secretKey,
			},
		})

		if err != nil {
			return errors.Wrap(err, "Error creating subscription")
		}

		if createSubResp.ErrorStatus > 0 {
			return errors.Errorf("Error creating subscription (%d) - %s", createSubResp.ErrorStatus, createSubResp.Error)
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

func escapeMarkdown(text string) string {
	r, err := regexp.Compile("([_*\\[\\]()~`>#+=|.!-])")
	if err != nil {
		panic(err)
	}
	return r.ReplaceAllString(text, "\\$1")
}

func scheduleRefresh(client *helix.Client, refreshToken string, expiresIn int) {
	expiresInDur := time.Duration(expiresIn) * time.Second
	log.Infof("Scheduling refreshing twitch client token in %d seconds at %s", expiresIn, time.Now().Add(expiresInDur).String())

	time.AfterFunc(expiresInDur, func() {
		log.Info("Refreshing twitch client token")
		refreshResp, err := client.RefreshUserAccessToken(refreshToken)
		if err != nil {
			err = errors.Wrap(err, "Unable to refresh app token")
			sentry.CaptureException(err)
			log.Panic(err)
		}
		scheduleRefresh(client, refreshResp.Data.RefreshToken, refreshResp.Data.ExpiresIn)
	})
}

func main() {
	err := sentry.Init(sentry.ClientOptions{
		Dsn:   os.Getenv("SENTRY_DSN"),
		Debug: true,
	})
	if err != nil {
		log.Fatalf("sentry.Init: %s", err)
	}
	log.Info("Sentry initialized")
	// Flush buffered events before the program terminates.
	defer sentry.Flush(2 * time.Second)

	err = realMain()
	if err != nil {
		sentry.CaptureException(err)
		log.Panic(err)
	}
}

func realMain() error {
	clientId := os.Getenv("TWITCH_CLIENT_ID")
	clientSecret := os.Getenv("TWITCH_CLIENT_SECRET")
	secretKey := os.Getenv("SECRETKEY")
	publicUrl := os.Getenv("PUBLIC_URL")
	goliveMessage := os.Getenv("GOLIVE_MESSAGE")
	discordWebhook := os.Getenv("DISCORD_WEBHOOK")
	airtableAPIKey := os.Getenv("AIRTABLE_API_KEY")
	airtableTableName := os.Getenv("AIRTABLE_TABLE_NAME")
	airtableBaseId := "app9gXc0ovBSGKOSE"

	if len(secretKey) == 0 {
		return errors.New("no secret key provided")
	}

	if len(airtableAPIKey) == 0 || len(airtableBaseId) == 0 || len(airtableTableName) == 0 {
		return errors.New("missing airtable config")
	}

	ds := discordsender.New(discordWebhook, goliveMessage)
	at := airtable.New(airtableAPIKey, airtableBaseId, airtableTableName)

	client, err := helix.NewClient(&helix.Options{
		ClientID:     clientId,
		ClientSecret: clientSecret,
	})
	if err != nil {
		return errors.Wrap(err, "Unable to create twitch client")
	}

	resp, err := client.RequestAppAccessToken([]string{"user:read:email"})
	if err != nil {
		return errors.Wrap(err, "Unable to request app token")
	}

	// Set the access token on the client
	client.SetAppAccessToken(resp.Data.AccessToken)
	scheduleRefresh(client, resp.Data.RefreshToken, resp.Data.ExpiresIn)

	port := ":3000"
	if os.Getenv("PORT") != "" {
		port = ":" + os.Getenv("PORT")
	}

	err = at.RegisterWebhook(fmt.Sprintf("%swebhook/airtable", publicUrl))
	if err != nil {
		return errors.Wrap(err, "Unable to register airtable webhook")
	}
	twitchusernames, err := at.Usernames()

	err = registerSubscription(secretKey, client, twitchusernames, publicUrl)
	if err != nil {
		return errors.Wrap(err, "Unable to create subscriptions")
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

	// Create an instance of sentryhttp
	sentryHandler := sentryhttp.New(sentryhttp.Options{
		Repanic: false,
	})

	log.Printf("server starting on %s\n", port)

	http.HandleFunc("/webhook/callbacks", sentryHandler.HandleFunc(handlerEventSub(secretKey, client, ds)))
	http.HandleFunc("/webhook/airtable", sentryHandler.HandleFunc(at.HttpHandler(func(usernames []string) {
		err = registerSubscription(secretKey, client, twitchusernames, publicUrl)
		if err != nil {
			panic(errors.Wrap(err, "Unable to create subscriptions"))
		}
	})))
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "\n")
	})

	handler := sentryhttp.New(sentryhttp.Options{}).Handle(http.DefaultServeMux)
	if err := http.ListenAndServe(port, handler); err != nil {
		return errors.Wrap(err, "unable to listen")
	}
	return nil
}
