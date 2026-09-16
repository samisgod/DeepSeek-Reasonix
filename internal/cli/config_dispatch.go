package cli

import "fmt"

// configCommand routes `reasonix config` subcommands. It lives beside its usage
// text rather than in cli.go so the dispatch entry point keeps its size budget.
func configCommand(args []string) int {
	if len(args) == 0 {
		configUsage()
		return 2
	}
	switch args[0] {
	case "auto-plan":
		return configAutoPlanCompatibilityCommand(args[1:])
	case "reasoning-language":
		return configReasoningLanguageCommand(args[1:])
	case "compact-ratio":
		return configCompactRatioCommand(args[1:])
	case "currency":
		return configCurrencyCommand(args[1:])
	case "telemetry":
		return configTelemetryCommand(args[1:])
	case "portable":
		return portableCommand(args[1:])
	default:
		configUsage()
		return 2
	}
}

func configUsage() {
	fmt.Print(`Usage:
  reasonix config reasoning-language [--local] [auto|zh|en]
  reasonix config compact-ratio [--local] [30..85]
  reasonix config currency [auto|CNY|USD]
  reasonix config telemetry [auto|on|off]
  reasonix config portable [on|off|status]
`)
}

func configTelemetryUsage() {
	fmt.Print(`Usage:
  reasonix config telemetry [auto|on|off]
`)
}

func configCompactRatioUsage() {
	fmt.Print(`Usage:
  reasonix config compact-ratio [--local] [30..85]
`)
}
