package interactions

import (
	. "github.com/Shunsui-EXT/KAORI-ROUTER/internal/constant"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/interfaces"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/translator/translator"
)

func init() {
	translator.Register(
		Interactions,
		Interactions,
		ConvertInteractionsRequestToInteractions,
		interfaces.TranslateResponse{
			Stream:    ConvertInteractionsResponsePassthrough,
			NonStream: ConvertInteractionsResponsePassthroughNonStream,
		},
	)
	translator.Register(
		Interactions,
		Gemini,
		ConvertInteractionsRequestToGemini,
		interfaces.TranslateResponse{
			Stream:    ConvertGeminiResponseToInteractions,
			NonStream: ConvertGeminiResponseToInteractionsNonStream,
		},
	)
	translator.Register(
		Gemini,
		Interactions,
		ConvertGeminiRequestToInteractions,
		interfaces.TranslateResponse{
			Stream:    ConvertInteractionsResponseToGemini,
			NonStream: ConvertInteractionsResponseToGeminiNonStream,
		},
	)
}
