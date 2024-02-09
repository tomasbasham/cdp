package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/chromedp/cdproto/har"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

func main() {
	h := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(h)

	opts := append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true), // This is actually already set but does not seem to work.
	)

	ctx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	// Create a new chromdp context which will be used to control the
	// browser. Cancelling the returned context will close a tab or an entire
	// browser,
	ctx, cancel = chromedp.NewContext(ctx)
	defer cancel()

	// Create a timeout to ensure the browser exits.
	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Create a channel to receive HAR data.
	harCh := make(chan interface{})

	// Intercept network events and marshal them to JSON.
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch ev := ev.(type) {
		case *network.EventRequestWillBeSent:
			go processRequest(ev, harCh)
		case *network.EventResponseReceived:
			go processResponse(ev, harCh)
		case *page.EventLifecycleEvent:
			// We determine the page to have finished loading when it has become fully
			// interactive.
			if ev.Name == "InteractiveTime" {
				close(harCh)
			}
		}
	})

	// Navigate to the webpage.
	err := chromedp.Run(ctx, chromedp.Navigate("https://google.com"))
	if err != nil {
		logger.Warn(fmt.Sprintf("error navigating: %s", err))
		return
	}

	harObject := har.HAR{
		Log: &har.Log{
			Version: "1.2",
			Browser: &har.Creator{
				Name:    "Google Chrome",
				Version: "90.0.4430.93", // TODO: Get this from the browser.
			},
			Creator: &har.Creator{
				Name:    "chromedp",
				Version: "0.1.0",
				Comment: "HAR capture",
			},
			Pages:   []*har.Page{},
			Entries: []*har.Entry{},
		},
	}

	// Wait for network events and construct HAR entries.
	for data := range harCh {
		switch data := data.(type) {
		case har.Page:
			harObject.Log.Pages = append(harObject.Log.Pages, &data)
		case har.Entry:
			harObject.Log.Entries = append(harObject.Log.Entries, &data)
		}
	}

	// Marshal the HAR object to JSON.
	harJSON, err := json.MarshalIndent(harObject, "", "  ")
	if err != nil {
		logger.Warn(fmt.Sprintf("error marshaling HAR object: %s", err))
		return
	}

	// Write the HAR JSON to a file.
	err = os.WriteFile("output.har", harJSON, 0o644)
	if err != nil {
		logger.Warn(fmt.Sprintf("error writing HAR file: %s", err))
		return
	}

	logger.Info("HAR file generated successfully.")
}

func processRequest(ev *network.EventRequestWillBeSent, harCh chan<- interface{}) {
	switch ev.Type {
	case network.ResourceTypeDocument:
		harCh <- har.Page{
			ID:              "page_" + string(ev.RequestID),
			StartedDateTime: ev.WallTime.Time().Format(time.RFC3339Nano),
			Title:           ev.Request.URL,
		}
	default:
		harCh <- har.Entry{
			Pageref:         "page_" + string(ev.RequestID),
			StartedDateTime: ev.WallTime.Time().Format(time.RFC3339Nano),
			// Time:            ev.Response.Time.Time().Sub(ev.Request.Time.Time()).Milliseconds(),
			Request: &har.Request{
				Method: ev.Request.Method,
				URL:    ev.Request.URL,
				// HTTPVersion: ev.Response.Protocol,
				Headers: headersToHAR(ev.Request.Headers),
			},
		}
	}
}

// https://github.com/ChromeDevTools/devtools-frontend/blob/29fab47578afb1ead4eb63414ec30cada4814b62/front_end/sdk/HARLog.js#L255-L329
func processResponse(ev *network.EventResponseReceived, harCh chan<- interface{}) {
	switch ev.Type {
	case network.ResourceTypeDocument:
		harCh <- har.Page{}
	default:
		harCh <- har.Entry{}
	}
}

func headersToHAR(headers network.Headers) []*har.NameValuePair {
	harHeaders := make([]*har.NameValuePair, 0, len(headers))
	for name, values := range map[string]interface{}(headers) {
		if arr, ok := values.([]string); ok {
			for _, value := range arr {
				harHeaders = append(harHeaders, &har.NameValuePair{
					Name:  name,
					Value: value,
				})
			}
		} else {
			harHeaders = append(harHeaders, &har.NameValuePair{
				Name:  name,
				Value: fmt.Sprint(values),
			})
		}
	}
	return harHeaders
}
