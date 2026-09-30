package claude

import (
	. "github.com/Shunsui-EXT/KAORI-ROUTER/internal/constant"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/interfaces"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/translator/translator"
)

func init() {
	translator.Register(
		Claude,
		Interactions,
		ConvertClaudeRequestToInteractions,
		interfaces.TranslateResponse{
			Stream:    ConvertInteractionsResponseToClaude,
			NonStream: ConvertInteractionsResponseToClaudeNonStream,
		},
	)
}
