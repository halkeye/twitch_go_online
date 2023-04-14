package discordsender

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"time"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

type DiscordSender struct {
	discordWebhook string
	tmpl           *template.Template
	lastBody       string
}

const (
	postMessageTmpl = `Look alive, mateys! {{.ChannelName}} is playing {{.Game}}
Channel URL: {{.ChannelUrl}}

Go give them some love!`
)

func New(discordWebhook string, goliveMessage string) *DiscordSender {
	if len(goliveMessage) == 0 {
		goliveMessage = postMessageTmpl
	}

	return &DiscordSender{
		discordWebhook: discordWebhook,
		tmpl:           template.Must(template.New("message").Parse(goliveMessage)),
	}
}

func (ds *DiscordSender) Send(tmplParams map[string]string) error {
	if len(ds.discordWebhook) == 0 {
		log.Info("No webhook setup, so bailing")
		return nil
	}

	var templateOutput bytes.Buffer
	err := ds.tmpl.Execute(&templateOutput, tmplParams)

	if err != nil {
		return errors.Wrap(err, "Error populating template")
	}

	tmplString := string(templateOutput.String())

	if ds.lastBody == tmplString {
		log.Info("Duplicate post body, skipping for now")
		return nil
	}

	ds.lastBody = tmplString

	jsonBody, err := json.Marshal(map[string]interface{}{"content": tmplString})
	if err != nil {
		return errors.Wrap(err, "unable to create json to send to discord")
	}
	bodyReader := bytes.NewReader(jsonBody)
	req, err := http.NewRequest(http.MethodPost, ds.discordWebhook, bodyReader)
	if err != nil {
		return errors.Wrap(err, "unable to create discord http client")
	}
	req.Header.Set("Content-Type", "application/json")

	client := http.Client{Timeout: 30 * time.Second}

	_, err = client.Do(req)
	if err != nil {
		return errors.Wrap(err, "posting to discord failed")
	}
	return nil
}
