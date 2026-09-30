package gemini

import (
	. "github.com/Shunsui-EXT/KAORI-ROUTER/internal/constant"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/interfaces"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/translator/translator"
)

func init() {
	translator.Register(
		Gemini,
		Codex,
		ConvertGeminiRequestToCodex,
		interfaces.TranslateResponse{
			Stream:     ConvertCodexResponseToGemini,
			NonStream:  ConvertCodexResponseToGeminiNonStream,
			TokenCount: GeminiTokenCount,
		},
	)
}
