package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gcal "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type Client struct {
	svc *gcal.Service
}

func Authorize(credFile, tokenFile string) (*Client, error) {
	b, err := os.ReadFile(credFile)
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}

	cfg, err := google.ConfigFromJSON(b, gcal.CalendarScope)
	if err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}

	tok, err := loadToken(tokenFile)
	if err != nil {
		return nil, fmt.Errorf("load token: %w", err)
	}

	svc, err := gcal.NewService(context.Background(),
		option.WithTokenSource(cfg.TokenSource(context.Background(), tok)))
	if err != nil {
		return nil, fmt.Errorf("create calendar service: %w", err)
	}

	return &Client{svc: svc}, nil
}

func (c *Client) CreateEvent(title, description string, start time.Time) (string, error) {
	end := start.Add(30 * time.Minute)
	event := &gcal.Event{
		Summary:     title,
		Description: description,
		Start:       &gcal.EventDateTime{DateTime: start.Format(time.RFC3339)},
		End:         &gcal.EventDateTime{DateTime: end.Format(time.RFC3339)},
	}
	created, err := c.svc.Events.Insert("primary", event).Do()
	if err != nil {
		return "", err
	}
	return created.Id, nil
}

func (c *Client) DeleteEvent(eventID string) error {
	if eventID == "" {
		return nil
	}
	return c.svc.Events.Delete("primary", eventID).Do()
}

func (c *Client) UpdateEvent(eventID, title, description string, start time.Time) error {
	if eventID == "" {
		return nil
	}
	end := start.Add(30 * time.Minute)
	event := &gcal.Event{
		Summary:     title,
		Description: description,
		Start:       &gcal.EventDateTime{DateTime: start.Format(time.RFC3339)},
		End:         &gcal.EventDateTime{DateTime: end.Format(time.RFC3339)},
	}
	_, err := c.svc.Events.Update("primary", eventID, event).Do()
	return err
}

func loadToken(path string) (*oauth2.Token, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var tok oauth2.Token
	if err := json.NewDecoder(f).Decode(&tok); err != nil {
		return nil, err
	}
	return &tok, nil
}
