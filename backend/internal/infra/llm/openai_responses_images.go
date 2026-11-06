package llm

import (
	"fmt"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

// GIF completion is buffered inside the image tool; Responses can still stream
// its outer lifecycle. Validate only the image tool's incompatible parameters.
func validateResponsesGIFImageTools(tools []map[string]any) error {
	for _, tool := range tools {
		if getString(tool["type"]) != "image_generation" || !openAIImageRequiresBufferedOutput(tool) {
			continue
		}
		if err := validateOpenAIImage26Params(getString(tool["model"]), portllm.GenerateInput{Options: tool}); err != nil {
			return err
		}
		for _, key := range []string{"input_image_mask", "output_compression"} {
			if value, configured := tool[key]; configured && value != nil {
				return fmt.Errorf("GIF image generation tool does not support %s", key)
			}
		}
		if raw, configured := tool["partial_images"]; configured && raw != nil {
			if value, valid := modelParamFloat(tool, "partial_images"); !valid || value != 0 {
				return fmt.Errorf("GIF image generation tool requires partial_images to be omitted or zero")
			}
		}
	}
	return nil
}
