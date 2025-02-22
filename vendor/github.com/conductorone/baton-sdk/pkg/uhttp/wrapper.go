package uhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/helpers"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type WrapperResponse struct {
	Header     http.Header
	Body       []byte
	Status     string
	StatusCode int
}

type (
	HttpClient interface {
		HttpClient() *http.Client
		Do(req *http.Request, options ...DoOption) (*http.Response, error)
		NewRequest(ctx context.Context, method string, url *url.URL, options ...RequestOption) (*http.Request, error)
	}
	BaseHttpClient struct {
		HttpClient *http.Client
	}

	DoOption      func(resp *WrapperResponse) error
	RequestOption func() (io.ReadWriter, map[string]string, error)
)

func NewBaseHttpClient(httpClient *http.Client) *BaseHttpClient {
	return &BaseHttpClient{
		HttpClient: httpClient,
	}
}

func WithJSONResponse(response interface{}) DoOption {
	return func(resp *WrapperResponse) error {
		return json.Unmarshal(resp.Body, response)
	}
}

type ErrorResponse interface {
	Message() string
}

func WithErrorResponse(resource ErrorResponse) DoOption {
	return func(resp *WrapperResponse) error {
		if resp.StatusCode < 300 {
			return nil
		}

		if !helpers.IsJSONContentType(resp.Header.Get("Content-Type")) {
			return fmt.Errorf("%v", string(resp.Body))
		}

		// Decode the JSON response body into the ErrorResponse
		if err := json.Unmarshal(resp.Body, &resource); err != nil {
			return status.Error(codes.Unknown, "Request failed with unknown error")
		}

		// Construct a more detailed error message
		errMsg := fmt.Sprintf("Request failed with status %d: %s", resp.StatusCode, resource.Message())

		return status.Error(codes.Unknown, errMsg)
	}
}

func WithRatelimitData(resource *v2.RateLimitDescription) DoOption {
	return func(resp *WrapperResponse) error {
		rl, err := helpers.ExtractRateLimitData(&resp.Header)
		if err != nil {
			return err
		}

		resource.Limit = rl.Limit
		resource.Remaining = rl.Remaining
		resource.ResetAt = rl.ResetAt

		return nil
	}
}

func (c *BaseHttpClient) Do(req *http.Request, options ...DoOption) (*http.Response, error) {
	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	err = resp.Body.Close()
	if err != nil {
		return nil, err
	}

	// Replace resp.Body with a no-op closer so nobody has to worry about closing the reader.
	resp.Body = io.NopCloser(bytes.NewBuffer(body))

	wresp := WrapperResponse{
		Header:     resp.Header,
		Status:     resp.Status,
		StatusCode: resp.StatusCode,
		Body:       body,
	}
	for _, option := range options {
		err = option(&wresp)
		if err != nil {
			return resp, err
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return resp, err
}

func WithJSONBody(body interface{}) RequestOption {
	return func() (io.ReadWriter, map[string]string, error) {
		buffer := new(bytes.Buffer)
		err := json.NewEncoder(buffer).Encode(body)
		if err != nil {
			return nil, nil, err
		}

		_, headers, err := WithContentTypeJSONHeader()()
		if err != nil {
			return nil, nil, err
		}

		return buffer, headers, nil
	}
}

func WithAcceptJSONHeader() RequestOption {
	return func() (io.ReadWriter, map[string]string, error) {
		return nil, map[string]string{
			"Accept": "application/json",
		}, nil
	}
}

func WithContentTypeJSONHeader() RequestOption {
	return func() (io.ReadWriter, map[string]string, error) {
		return nil, map[string]string{
			"Content-Type": "application/json",
		}, nil
	}
}

func (c *BaseHttpClient) NewRequest(ctx context.Context, method string, url *url.URL, options ...RequestOption) (*http.Request, error) {
	var buffer io.ReadWriter
	var headers map[string]string = make(map[string]string)
	for _, option := range options {
		buf, h, err := option()
		if err != nil {
			return nil, err
		}

		if buf != nil {
			buffer = buf
		}

		for k, v := range h {
			headers[k] = v
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url.String(), buffer)
	if err != nil {
		return nil, err
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return req, nil
}
