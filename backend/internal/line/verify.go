package line

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const idTokenVerifyURL = "https://api.line.me/oauth2/v2.1/verify"

type Profile struct {
	UserID string
	Name   string
}

func VerifyIDToken(ctx context.Context, client *http.Client, idToken, channelID string) (Profile, error) {
	idToken = strings.TrimSpace(idToken)
	channelID = strings.TrimSpace(channelID)
	if idToken == "" || channelID == "" {
		return Profile{}, errors.New("missing id token")
	}
	if client == nil {
		client = http.DefaultClient
	}
	form := url.Values{}
	form.Set("id_token", idToken)
	form.Set("client_id", channelID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, idTokenVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Profile{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return Profile{}, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Profile{}, err
	}
	if resp.StatusCode >= 300 {
		return Profile{}, fmt.Errorf("line verify status %d", resp.StatusCode)
	}
	var body struct {
		Sub  string `json:"sub"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return Profile{}, err
	}
	body.Sub = strings.TrimSpace(body.Sub)
	if body.Sub == "" {
		return Profile{}, errors.New("line verify missing sub")
	}
	return Profile{UserID: body.Sub, Name: strings.TrimSpace(body.Name)}, nil
}
