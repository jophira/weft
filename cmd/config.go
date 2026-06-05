package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/jophira/weft/internal/config"
	"github.com/jophira/weft/internal/validate"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "View and change weft configuration",
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print the current value of a config key",
	Long: `Print the current value of a config key.

Supported keys:
  warn-size    instruction file size threshold in KB (default: 96)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "warn-size":
			kb := viper.GetInt("warn_instruction_size_kb")
			if kb <= 0 {
				kb = validate.DefaultWarnSizeKB
			}
			fmt.Printf("warn-size: %d KB\n", kb)
		default:
			return fmt.Errorf("unknown key %q — supported keys: warn-size", args[0])
		}
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a config value and persist it to config.yaml",
	Long: `Set a config value and persist it to ~/.config/weft/config.yaml.

Supported keys:
  warn-size    instruction file size threshold in KB (default: 96)
               weft warns when a merged CLAUDE.md exceeds this size`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key, raw := args[0], args[1]
		switch key {
		case "warn-size":
			kb, err := strconv.Atoi(raw)
			if err != nil || kb <= 0 {
				return fmt.Errorf("warn-size must be a positive integer (KB), got %q", raw)
			}
			if err := config.SetWarnInstructionSizeKB(kb); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}
			fmt.Printf("warn-size set to %d KB\n", kb)
		default:
			return fmt.Errorf("unknown key %q — supported keys: warn-size", key)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configGetCmd, configSetCmd)
}
