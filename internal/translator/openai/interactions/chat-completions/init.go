package chat_completions

import (
	. "github.com/Shunsui-EXT/KAORI-ROUTER/internal/constant"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/interfaces"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/translator/translator"
)

func init() {
	translator.Register(
		OpenAI,
		Interactions,
		ConvertOpenAIRequestToInteractions,
		interfaces.TranslateResponse{
			Stream:    ConvertInteractionsResponseToOpenAI,
			NonStream: ConvertInteractionsResponseToOpenAINonStream,
		},
	)
	translator.Register(
		Interactions,
		OpenAI,
		ConvertInteractionsRequestToOpenAI,
		interfaces.TranslateResponse{
			Stream:    ConvertOpenAIResponseToInteractions,
			NonStream: ConvertOpenAIResponseToInteractionsNonStream,
		},
	)
}
