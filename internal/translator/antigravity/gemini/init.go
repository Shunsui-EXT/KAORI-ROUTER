package gemini

import (
	. "github.com/Shunsui-EXT/KAORI-ROUTER/internal/constant"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/interfaces"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/translator/translator"
)

func init() {
	translator.Register(
		Gemini,
		Antigravity,
		ConvertGeminiRequestToAntigravity,
		interfaces.TranslateResponse{
			Stream:     ConvertAntigravityResponseToGemini,
			NonStream:  ConvertAntigravityResponseToGeminiNonStream,
			TokenCount: GeminiTokenCount,
		},
	)
}
