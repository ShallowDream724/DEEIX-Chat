package conversation

import "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"

// Tool support and Files support are separate model capabilities. Reuse the
// dispatch resolver so route changes also update wire names and usage identity.
func restrictDeepSeekToolsForRoute(route *channel.ResolvedRoute, options map[string]any) map[string]any {
	if route == nil || len(providerToolOptionPayloads(options["tools"])) == 0 {
		return options
	}
	tools := nativeProviderToolsFromOption(modelOptionPolicyProtocolKey(route.Protocol), options["tools"], route.ModelCapabilitiesJSON, route.UpstreamModel)
	result := cloneModelOptionMap(options)
	if len(tools) == 0 {
		delete(result, "tools")
	} else {
		result["tools"] = tools
	}
	return result
}
