package config

// CommandCode Provider API (https://commandcode.ai/provider) serves OpenAI Chat
// Completions, OpenAI Responses and Anthropic Messages behind one Bearer-
// authenticated gateway. A single base URL carries every format, but endpoints
// and model families are bound: Claude models must use /messages while OpenAI
// and open-weight models use /chat/completions or /responses (the gateway
// rejects a mismatch with 400). Each format is therefore installed as its own
// preset, mirroring Doubao, Fireworks and xAI. Model identifiers copy the
// published registry at https://commandcode.ai/docs/reference/cli/models.
const commandCodeGatewayBaseURL = "https://api.commandcode.ai/provider/v1"

var commandCodeClaudeModels = []string{
	"claude-sonnet-5",
	"claude-sonnet-4-6",
	"claude-opus-5",
	"claude-opus-4-8",
	"claude-opus-4-7",
	"claude-haiku-4-5",
	"claude-fable-5-1",
	"claude-fable-5",
}

// Every Claude model on the gateway is multimodal; the registry only tags a
// subset with its extra capability chip.
var commandCodeClaudeVisionModels = commandCodeClaudeModels

var commandCodeOpenModels = []string{
	"deepseek/deepseek-v4-flash",
	"deepseek/deepseek-v4-flash-fast",
	"deepseek/deepseek-v4-pro",
	"deepseek/deepseek-v4.1-flash",
	"deepseek/deepseek-v4-flash-vision-exp",
	"moonshotai/Kimi-K3",
	"moonshotai/Kimi-K2.7-Code",
	"moonshotai/Kimi-K2.6",
	"zai-org/GLM-5.3",
	"zai-org/GLM-5.2",
	"MiniMaxAI/MiniMax-M3",
	"Qwen/Qwen3.8-Max",
	"Qwen/Qwen3.8-27B",
	"gpt-5.5",
	"gpt-5.4",
	"google/gemini-3.8-flash",
	"xai/grok-4.6",
}

var commandCodeOpenVisionModels = []string{
	"deepseek/deepseek-v4-flash-vision-exp",
	"Qwen/Qwen3.8-27B",
	"gpt-5.5",
	"gpt-5.4",
	"google/gemini-3.8-flash",
}

var commandCodePresets = []ProviderPreset{
	{
		ID:          "commandcode-chat",
		Label:       "CommandCode Chat Completions",
		Description: "CommandCode Provider API Chat Completions format for OpenAI and open-weight models.",
		KeyEnv:      "COMMANDCODE_API_KEY",
		Entries: []ProviderEntry{{
			Name:         "commandcode-chat",
			Kind:         "openai",
			BaseURL:      commandCodeGatewayBaseURL,
			ModelsURL:    commandCodeGatewayBaseURL + "/models",
			Models:       commandCodeOpenModels,
			VisionModels: commandCodeOpenVisionModels,
			Default:      "deepseek/deepseek-v4-flash",
			APIKeyEnv:    "COMMANDCODE_API_KEY",
			WebSearch:    boolPointer(false),
		}},
	},
	{
		ID:          "commandcode-anthropic",
		Label:       "CommandCode Anthropic",
		Description: "CommandCode Provider API Anthropic Messages format for Claude models with Bearer auth.",
		KeyEnv:      "COMMANDCODE_API_KEY",
		Entries: []ProviderEntry{{
			Name:         "commandcode-anthropic",
			Kind:         "anthropic",
			BaseURL:      commandCodeGatewayBaseURL,
			ModelsURL:    commandCodeGatewayBaseURL + "/models",
			Models:       commandCodeClaudeModels,
			VisionModels: commandCodeClaudeVisionModels,
			Default:      "claude-sonnet-5",
			APIKeyEnv:    "COMMANDCODE_API_KEY",
			AuthHeader:   true,
			WebSearch:    boolPointer(false),
		}},
	},
	{
		ID:          "commandcode-responses",
		Label:       "CommandCode Responses",
		Description: "CommandCode Provider API Responses format for OpenAI and open-weight models.",
		KeyEnv:      "COMMANDCODE_API_KEY",
		Entries: []ProviderEntry{{
			Name:          "commandcode-responses",
			Kind:          "responses",
			BaseURL:       commandCodeGatewayBaseURL,
			ModelsURL:     commandCodeGatewayBaseURL + "/models",
			Models:        commandCodeOpenModels,
			VisionModels:  commandCodeOpenVisionModels,
			Default:       "deepseek/deepseek-v4-flash",
			APIKeyEnv:     "COMMANDCODE_API_KEY",
			ResponsesMode: "stateless",
			WebSearch:     boolPointer(false),
		}},
	},
}

func init() {
	curatedProviderPresets = append(curatedProviderPresets, commandCodePresets...)
}
