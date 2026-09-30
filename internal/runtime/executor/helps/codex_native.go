package helps

import (
	"strings"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/util"
	cliproxyexecutor "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/executor"
	sdktranslator "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/translator"
)

// IsNativeCodexRequest checks the client dialect for use inside Codex executors.
func IsNativeCodexRequest(body []byte, opts cliproxyexecutor.Options) bool {
	for _, format := range []sdktranslator.Format{opts.SourceFormat, cliproxyexecutor.ResponseFormatOrSource(opts)} {
		name := strings.TrimSpace(format.String())
		if !strings.EqualFold(name, sdktranslator.FormatCodex.String()) && !strings.EqualFold(name, sdktranslator.FormatOpenAIResponse.String()) {
			return false
		}
	}
	return util.IsCodexResponsesLiteRequest(body, opts.Headers)
}
