// Package google implements [stream.Stream] for Google.
package google

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/charmbracelet/mods/internal/proto"
	"github.com/charmbracelet/mods/internal/stream"
	"github.com/openai/openai-go"
)

var _ stream.Client = &Client{}

// Config represents the configuration for the Google API client.
type Config struct {
	BaseURL        string
	HTTPClient     *http.Client
	ThinkingBudget int
	AuthToken      string // JWT bearer token for Vertex AI
	UseVertexAPI   bool   // Flag to distinguish between AI Studio and Vertex AI
}

// DefaultConfig returns the default configuration for the Google API client.
// It automatically detects whether to use AI Studio or Vertex AI based on the authToken.
func DefaultConfig(model, authToken string) Config {
	// Check if this looks like a JWT token (contains dots) vs API key
	isJWT := len(authToken) > 100 && strings.Contains(authToken, ".")
	
	if isJWT {
		// Use Vertex AI with JWT token
		// You can replace this URL with your fixed Vertex URL
		return Config{
			BaseURL:      fmt.Sprintf("https://us-central1-aiplatform.googleapis.com/v1/projects/YOUR_PROJECT/locations/us-central1/publishers/google/models/%s:generateContent", model),
			HTTPClient:   &http.Client{},
			AuthToken:    authToken,
			UseVertexAPI: true,
		}
	} else {
		// Use AI Studio with API key
		return Config{
			BaseURL:      fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, authToken),
			HTTPClient:   &http.Client{},
			UseVertexAPI: false,
		}
	}
}

// Part is a datatype containing media that is part of a multi-part Content message.
type Part struct {
	Text string `json:"text,omitempty"`
}

// Content is the base structured datatype containing multi-part content of a message.
type Content struct {
	Parts []Part `json:"parts,omitempty"`
	Role  string `json:"role,omitempty"`
}

// ThinkingConfig - for more details see https://ai.google.dev/gemini-api/docs/thinking#rest .
type ThinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget,omitempty"`
}

// GenerationConfig are the options for model generation and outputs. Not all parameters are configurable for every model.
type GenerationConfig struct {
	StopSequences    []string        `json:"stopSequences,omitempty"`
	ResponseMimeType string          `json:"responseMimeType,omitempty"`
	CandidateCount   uint            `json:"candidateCount,omitempty"`
	MaxOutputTokens  uint            `json:"maxOutputTokens,omitempty"`
	Temperature      float64         `json:"temperature,omitempty"`
	TopP             float64         `json:"topP,omitempty"`
	TopK             int64           `json:"topK,omitempty"`
	ThinkingConfig   *ThinkingConfig `json:"thinkingConfig,omitempty"`
}

// MessageCompletionRequest represents the valid parameters and value options for the request.
type MessageCompletionRequest struct {
	Contents         []Content        `json:"contents,omitempty"`
	GenerationConfig GenerationConfig `json:"generationConfig,omitempty"`
}

// RequestBuilder is an interface for building HTTP requests for the Google API.
type RequestBuilder interface {
	Build(ctx context.Context, method, url string, body any, header http.Header) (*http.Request, error)
}

// NewRequestBuilder creates a new HTTPRequestBuilder.
func NewRequestBuilder() *HTTPRequestBuilder {
	return &HTTPRequestBuilder{
		marshaller: &JSONMarshaller{},
	}
}

// Client is a client for the Google API.
type Client struct {
	config Config

	requestBuilder RequestBuilder
}

// Request implements stream.Client.
func (c *Client) Request(ctx context.Context, request proto.Request) stream.Stream {
	stream := new(Stream)
	body := MessageCompletionRequest{
		Contents: fromProtoMessages(request.Messages),
		GenerationConfig: GenerationConfig{
			ResponseMimeType: "",
			CandidateCount:   1,
			StopSequences:    request.Stop,
			MaxOutputTokens:  4096,
		},
	}

	if request.Temperature != nil {
		body.GenerationConfig.Temperature = *request.Temperature
	}
	if request.TopP != nil {
		body.GenerationConfig.TopP = *request.TopP
	}
	if request.TopK != nil {
		body.GenerationConfig.TopK = *request.TopK
	}

	if request.MaxTokens != nil {
		body.GenerationConfig.MaxOutputTokens = uint(*request.MaxTokens) //nolint:gosec
	}

	if c.config.ThinkingBudget != 0 {
		body.GenerationConfig.ThinkingConfig = &ThinkingConfig{
			ThinkingBudget: c.config.ThinkingBudget,
		}
	}

	req, err := c.newRequest(ctx, http.MethodPost, c.config.BaseURL, withBody(body))
	if err != nil {
		stream.err = err
		return stream
	}

	stream, err = googleSendRequestStream(c, req, request.Messages)
	if err != nil {
		stream.err = err
	}
	return stream
}

// New creates a new Client with the given configuration.
func New(config Config) *Client {
	return &Client{
		config:         config,
		requestBuilder: NewRequestBuilder(),
	}
}

func (c *Client) newRequest(ctx context.Context, method, url string, setters ...requestOption) (*http.Request, error) {
	// Default Options
	args := &requestOptions{
		body:   MessageCompletionRequest{},
		header: make(http.Header),
	}
	for _, setter := range setters {
		setter(args)
	}
	req, err := c.requestBuilder.Build(ctx, method, url, args.body, args.header)
	if err != nil {
		return new(http.Request), err
	}
	return req, nil
}

func (c *Client) handleErrorResp(resp *http.Response) error {
	// Print the response text
	var errRes openai.Error
	if err := json.NewDecoder(resp.Body).Decode(&errRes); err != nil {
		return &openai.Error{
			StatusCode: resp.StatusCode,
			Message:    err.Error(),
		}
	}
	errRes.StatusCode = resp.StatusCode
	return &errRes
}

// Candidate represents a response candidate generated from the model.
type Candidate struct {
	Content      Content `json:"content,omitempty"`
	FinishReason string  `json:"finishReason,omitempty"`
	TokenCount   uint    `json:"tokenCount,omitempty"`
	Index        uint    `json:"index,omitempty"`
}

// CompletionMessageResponse represents a response to an Google completion message.
type CompletionMessageResponse struct {
	Candidates []Candidate `json:"candidates,omitempty"`
}

// Stream struct represents a stream of messages from the Google API.
type Stream struct {
	isFinished bool
	content    string
	hasContent bool
	messages   []proto.Message

	response *http.Response
	err      error

	httpHeader
}

// CallTools implements stream.Stream.
func (s *Stream) CallTools() []proto.ToolCallStatus {
	// No tool calls in Gemini/Google API yet.
	return nil
}

// Err implements stream.Stream.
func (s *Stream) Err() error { return s.err }

// Messages implements stream.Stream.
func (s *Stream) Messages() []proto.Message {
	return s.messages
}

// Next implements stream.Stream.
func (s *Stream) Next() bool {
	return !s.isFinished
}

// Close closes the stream.
func (s *Stream) Close() error {
	if s.response != nil && s.response.Body != nil {
		return s.response.Body.Close() //nolint:wrapcheck
	}
	return nil
}

// Current implements stream.Stream.
func (s *Stream) Current() (proto.Chunk, error) {
	if !s.hasContent {
		s.isFinished = true
		return proto.Chunk{}, stream.ErrNoContent
	}
	
	s.hasContent = false
	s.isFinished = true
	
	return proto.Chunk{
		Content: s.content,
	}, nil
}

func googleSendRequestStream(client *Client, req *http.Request, originalMessages []proto.Message) (*Stream, error) {
	req.Header.Set("content-type", "application/json")
	
	// Set authentication header based on API type
	if client.config.UseVertexAPI {
		req.Header.Set("Authorization", "Bearer "+client.config.AuthToken)
	}

	resp, err := client.config.HTTPClient.Do(req) //nolint:bodyclose // body is closed in stream.Close()
	if err != nil {
		return new(Stream), err
	}
	if isFailureStatusCode(resp) {
		return new(Stream), client.handleErrorResp(resp)
	}

	// Read the complete response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return new(Stream), fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse the JSON response
	var response CompletionMessageResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return new(Stream), fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Extract content from the response
	var content string
	if len(response.Candidates) > 0 && len(response.Candidates[0].Content.Parts) > 0 {
		content = response.Candidates[0].Content.Parts[0].Text
	}

	// Build complete conversation messages including the assistant's response
	messages := make([]proto.Message, len(originalMessages))
	copy(messages, originalMessages)
	
	if content != "" {
		messages = append(messages, proto.Message{
			Role:    proto.RoleAssistant,
			Content: content,
		})
	}

	return &Stream{
		content:    content,
		hasContent: content != "",
		messages:   messages,
		response:   resp,
		httpHeader: httpHeader(resp.Header),
	}, nil
}
