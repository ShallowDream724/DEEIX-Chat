package llm

import (
	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

func routeInputLimits(route portllm.RouteConfig) inputlimits.Limits {
	if route.InputLimits != nil {
		return *route.InputLimits
	}
	return inputlimits.Resolve("", route.Protocol, route.BaseURL, 0)
}

func checkInputBudget(name string, actual, limit int64) error {
	if limit > 0 && actual > limit {
		return portllm.NewInputLimitError(name, actual, limit)
	}
	return nil
}

// validateEncodedRequestSize runs after serialization, before HTTP tracing or
// transport acceptance. It uses the request's known length without reading its
// body, so JSON and multipart requests retain their normal replay behavior.
func validateEncodedRequestSize(route portllm.RouteConfig, size int64) error {
	if size < 0 && (routeInputLimits(route).RequestBytes > 0 || inputlimits.Provider(route.Protocol, route.BaseURL, route.ContextWindowTokens).RequestBytes > 0) {
		limit := routeInputLimits(route).RequestBytes
		if limit <= 0 {
			limit = inputlimits.Provider(route.Protocol, route.BaseURL, route.ContextWindowTokens).RequestBytes
		}
		return portllm.NewInputLimitError("requestBytes", -1, limit)
	}
	if err := checkInputBudget("requestBytes", size, routeInputLimits(route).RequestBytes); err != nil {
		return err
	}
	return checkInputBudget("provider.requestBytes", size, inputlimits.Provider(route.Protocol, route.BaseURL, route.ContextWindowTokens).RequestBytes)
}

// validateInputMedia counts each occurrence in the actual model messages.
// Repeated attachments consume request space each time they are serialized.
func validateInputMedia(route portllm.RouteConfig, input portllm.GenerateInput) error {
	if err := portllm.ValidateInputMedia(routeInputLimits(route), input); err != nil {
		return err
	}
	return portllm.ValidateProviderInput(route, input)
}
