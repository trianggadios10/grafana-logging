package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// TracedHTTPClient wraps an HTTP client with OpenTelemetry instrumentation
type TracedHTTPClient struct {
	client *http.Client
}

// NewTracedHTTPClient creates a new HTTP client with tracing
func NewTracedHTTPClient(timeout time.Duration) *TracedHTTPClient {
	return &TracedHTTPClient{
		client: &http.Client{
			Timeout: timeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport,
				otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
					return fmt.Sprintf("HTTP %s %s", r.Method, r.URL.Host)
				}),
			),
		},
	}
}

// Get performs a GET request with tracing
func (c *TracedHTTPClient) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	return c.client.Do(req)
}

// Do performs an HTTP request with tracing
func (c *TracedHTTPClient) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	req = req.WithContext(ctx)
	return c.client.Do(req)
}

// WeatherClient fetches weather data from wttr.in
type WeatherClient struct {
	httpClient *TracedHTTPClient
	baseURL    string
}

// NewWeatherClient creates a new weather client
func NewWeatherClient(timeout time.Duration) *WeatherClient {
	return &WeatherClient{
		httpClient: NewTracedHTTPClient(timeout),
		baseURL:    "https://wttr.in",
	}
}

// WeatherResponse represents weather data
type WeatherResponse struct {
	Location    string `json:"location"`
	Temperature string `json:"temperature"`
	Condition   string `json:"condition"`
	Humidity    string `json:"humidity"`
	Wind        string `json:"wind"`
	RawData     string `json:"raw_data,omitempty"`
}

// GetWeather fetches weather for a location
func (c *WeatherClient) GetWeather(ctx context.Context, location string) (*WeatherResponse, error) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("weather.location", location),
		attribute.String("weather.provider", "wttr.in"),
	)

	url := fmt.Sprintf("%s/%s?format=j1", c.baseURL, location)
	resp, err := c.httpClient.Get(ctx, url)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to fetch weather: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weather API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse the JSON response from wttr.in
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		// If JSON parsing fails, return raw text (wttr.in sometimes returns text format)
		return &WeatherResponse{
			Location: location,
			RawData:  string(body),
		}, nil
	}

	weather := &WeatherResponse{
		Location: location,
	}

	// Extract relevant fields from wttr.in JSON response
	if currentCondition, ok := data["current_condition"].([]interface{}); ok && len(currentCondition) > 0 {
		if cc, ok := currentCondition[0].(map[string]interface{}); ok {
			if temp, ok := cc["temp_C"].(string); ok {
				weather.Temperature = temp + "°C"
			}
			if humidity, ok := cc["humidity"].(string); ok {
				weather.Humidity = humidity + "%"
			}
			if windspeed, ok := cc["windspeedKmph"].(string); ok {
				weather.Wind = windspeed + " km/h"
			}
			if desc, ok := cc["weatherDesc"].([]interface{}); ok && len(desc) > 0 {
				if d, ok := desc[0].(map[string]interface{}); ok {
					if val, ok := d["value"].(string); ok {
						weather.Condition = val
					}
				}
			}
		}
	}

	span.SetAttributes(
		attribute.String("weather.temperature", weather.Temperature),
		attribute.String("weather.condition", weather.Condition),
	)

	return weather, nil
}

// QuoteClient fetches quotes from quotable.io
type QuoteClient struct {
	httpClient *TracedHTTPClient
	baseURL    string
}

// NewQuoteClient creates a new quote client
func NewQuoteClient(timeout time.Duration) *QuoteClient {
	return &QuoteClient{
		httpClient: NewTracedHTTPClient(timeout),
		baseURL:    "https://api.quotable.io",
	}
}

// Quote represents a quote from quotable.io
type Quote struct {
	ID           string   `json:"_id"`
	Content      string   `json:"content"`
	Author       string   `json:"author"`
	AuthorSlug   string   `json:"authorSlug"`
	Tags         []string `json:"tags"`
	Length       int      `json:"length"`
	DateAdded    string   `json:"dateAdded"`
	DateModified string   `json:"dateModified"`
}

// GetRandomQuote fetches a random quote
func (c *QuoteClient) GetRandomQuote(ctx context.Context) (*Quote, error) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("quote.provider", "quotable.io"),
	)

	url := fmt.Sprintf("%s/random", c.baseURL)
	resp, err := c.httpClient.Get(ctx, url)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to fetch quote: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("quote API returned status %d", resp.StatusCode)
	}

	var quote Quote
	if err := json.NewDecoder(resp.Body).Decode(&quote); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to decode quote: %w", err)
	}

	span.SetAttributes(
		attribute.String("quote.author", quote.Author),
		attribute.Int("quote.length", quote.Length),
	)

	return &quote, nil
}

// GetQuotesByTag fetches quotes by tag
func (c *QuoteClient) GetQuotesByTag(ctx context.Context, tag string, limit int) ([]Quote, error) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("quote.provider", "quotable.io"),
		attribute.String("quote.tag", tag),
		attribute.Int("quote.limit", limit),
	)

	url := fmt.Sprintf("%s/quotes?tags=%s&limit=%d", c.baseURL, tag, limit)
	resp, err := c.httpClient.Get(ctx, url)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to fetch quotes: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("quote API returned status %d", resp.StatusCode)
	}

	var response struct {
		Count      int     `json:"count"`
		TotalCount int     `json:"totalCount"`
		Results    []Quote `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to decode quotes: %w", err)
	}

	span.SetAttributes(
		attribute.Int("quote.results_count", len(response.Results)),
	)

	return response.Results, nil
}
