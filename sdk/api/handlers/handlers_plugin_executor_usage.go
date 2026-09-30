package handlers

import (
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/runtime/executor/helps"
	"github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/usage"
)

func parsePluginExecutorResponseUsage(protocol string, payload []byte) usage.Detail {
	return helps.ParsePluginExecutorResponseUsage(protocol, payload)
}

func observePluginExecutorStreamUsage(protocol string, payload []byte, buffer *helps.StreamUsageBuffer) {
	helps.ObservePluginExecutorStreamUsage(protocol, payload, buffer)
}
