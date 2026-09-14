package helps

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/tidwall/gjson"
)

func recordUpstreamMetadata(ginCtx *gin.Context, info UpstreamRequestLog) {
	if provider := strings.TrimSpace(info.Provider); provider != "" {
		ginCtx.Set(logging.UpstreamProviderContextKey, provider)
	}
	if model := strings.TrimSpace(gjson.GetBytes(info.Body, "model").String()); model != "" {
		ginCtx.Set(logging.UpstreamModelContextKey, model)
	}
}
