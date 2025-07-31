package tableau

import (
	"context"
	"io"
	"net/http"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
)

func logBody(ctx context.Context, res *http.Response) {
	if res == nil || res.Body == nil {
		return
	}
	l := ctxzap.Extract(ctx)
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		l.Error("Failed to read response body", zap.Error(err))
		return
	}

	l.Info("Response body",
		zap.String("status", res.Status),
		zap.ByteString("body", body),
	)
}
