package interactions

import (
	. "github.com/Shunsui-EXT/KAORI-ROUTER/internal/constant"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/interfaces"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/translator/translator"
)

func init() {
	translator.Register(
		Interactions,
		Claude,
		ConvertInteractionsRequestToClaude,
		interfaces.TranslateResponse{
			Stream:    ConvertClaudeResponseToInteractions,
			NonStream: ConvertClaudeResponseToInteractionsNonStream,
		},
	)
}
