package command

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/bflad/tfproviderdocs/check"
	tfjson "github.com/hashicorp/terraform-json"
	"github.com/mitchellh/cli"
)

type CheckCommandConfig struct {
	AllowedGuideSubcategories        string
	AllowedGuideSubcategoriesFile    string
	AllowedResourceSubcategories     string
	AllowedResourceSubcategoriesFile string
	EnableContentsCheck              bool
	IgnoreCdktfMissingFiles          bool
	IgnoreFileMismatchDataSources    string
	IgnoreFileMismatchResources      string
	IgnoreFileMissingDataSources     string
	IgnoreFileMissingResources       string
	LogLevel                         string
	Path                             string
	ProviderName                     string
	ProviderSource                   string
	ProvidersSchemaJson              string
	RequireGuideSubcategory          bool
	RequireResourceSubcategory       bool
	RequireSchemaOrdering            bool
}

// CheckCommand is a Command implementation
type CheckCommand struct {
	Ui cli.Ui
}

func (*CheckCommand) Help() string {
	optsBuffer := bytes.NewBuffer([]byte{})
	opts := tabwriter.NewWriter(optsBuffer, 0, 0, 1, ' ', 0)
	LogLevelFlagHelp(opts)
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-allowed-guide-subcategories", "Comma separated list of allowed guide frontmatter subcategories.")
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-allowed-guide-subcategories-file", "Path to newline separated file of allowed guide frontmatter subcategories.")
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-allowed-resource-subcategories", "Comma separated list of allowed data source and resource frontmatter subcategories.")
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-allowed-resource-subcategories-file", "Path to newline separated file of allowed data source and resource frontmatter subcategories.")
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-enable-contents-check", "(Experimental) Enable contents checking.")
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-ignore-cdktf-missing-files", "Ignore checks for missing CDK for Terraform documentation files when iteratively introducing them in large providers.")
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-ignore-file-mismatch-data-sources", "Comma separated list of data sources to ignore mismatched/extra files.")
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-ignore-file-mismatch-resources", "Comma separated list of resources to ignore mismatched/extra files.")
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-ignore-file-missing-data-sources", "Comma separated list of data sources to ignore missing files.")
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-ignore-file-missing-resources", "Comma separated list of resources to ignore missing files.")
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-provider-name", "Terraform Provider short name (e.g. aws). Automatically determined if -provider-source is given or if current working directory or provided path is prefixed with terraform-provider-*.")
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-provider-source", "Terraform Provider source address (e.g. registry.terraform.io/hashicorp/aws) for Terraform CLI 0.13 and later -providers-schema-json. Automatically sets -provider-name by dropping hostname and namespace prefix.")
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-providers-schema-json", "Path to terraform providers schema -json file. Enables enhanced validations.")
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-require-guide-subcategory", "Require guide frontmatter subcategory.")
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-require-resource-subcategory", "Require data source and resource frontmatter subcategory.")
	fmt.Fprintf(opts, CommandHelpOptionFormat, "-require-schema-ordering", "Require schema attribute lists to be alphabetically ordered (requires -enable-contents-check).")
	opts.Flush()

	helpText := fmt.Sprintf(`
Usage: tfproviderdocs check [options] [PATH]

  Performs documentation directory and file checks against the given Terraform Provider codebase.

Options:

%s
`, optsBuffer.String())

	return strings.TrimSpace(helpText)
}

func (c *CheckCommand) Name() string { return "check" }

func (c *CheckCommand) Run(args []string) int {
	var config CheckCommandConfig

	flags := flag.NewFlagSet(c.Name(), flag.ContinueOnError)
	flags.Usage = func() { c.Ui.Info(c.Help()) }
	LogLevelFlag(flags, &config.LogLevel)
	flags.StringVar(&config.AllowedGuideSubcategories, "allowed-guide-subcategories", "", "")
	flags.StringVar(&config.AllowedGuideSubcategoriesFile, "allowed-guide-subcategories-file", "", "")
	flags.StringVar(&config.AllowedResourceSubcategories, "allowed-resource-subcategories", "", "")
	flags.StringVar(&config.AllowedResourceSubcategoriesFile, "allowed-resource-subcategories-file", "", "")
	flags.BoolVar(&config.EnableContentsCheck, "enable-contents-check", false, "")
	flags.BoolVar(&config.IgnoreCdktfMissingFiles, "ignore-cdktf-missing-files", false, "")
	flags.StringVar(&config.IgnoreFileMismatchDataSources, "ignore-file-mismatch-data-sources", "", "")
	flags.StringVar(&config.IgnoreFileMismatchResources, "ignore-file-mismatch-resources", "", "")
	flags.StringVar(&config.IgnoreFileMissingDataSources, "ignore-file-missing-data-sources", "", "")
	flags.StringVar(&config.IgnoreFileMissingResources, "ignore-file-missing-resources", "", "")
	flags.StringVar(&config.ProviderName, "provider-name", "", "")
	flags.StringVar(&config.ProviderSource, "provider-source", "", "")
	flags.StringVar(&config.ProvidersSchemaJson, "providers-schema-json", "", "")
	flags.BoolVar(&config.RequireGuideSubcategory, "require-guide-subcategory", false, "")
	flags.BoolVar(&config.RequireResourceSubcategory, "require-resource-subcategory", false, "")
	flags.BoolVar(&config.RequireSchemaOrdering, "require-schema-ordering", false, "")

	if err := flags.Parse(args); err != nil {
		flags.Usage()
		return 1
	}

	args = flags.Args()

	if len(args) == 1 {
		config.Path = args[0]
	}

	ConfigureLogging(c.Name(), config.LogLevel)

	if config.ProviderName == "" && config.ProviderSource != "" {
		providerSourceParts := strings.Split(config.ProviderSource, "/")
		config.ProviderName = providerSourceParts[len(providerSourceParts)-1]
	}

	if config.ProviderName == "" {
		if config.Path == "" {
			config.ProviderName = providerNameFromCurrentDirectory()
		} else {
			config.ProviderName = providerNameFromPath(config.Path)
		}
	}

	if config.ProviderName == "" {
		log.Printf("[WARN] Unable to determine provider name. Contents and enhanced validations may fail.")
	} else {
		log.Printf("[DEBUG] Found provider name: %s", config.ProviderName)
	}

	directories, err := check.GetDirectories(config.Path)

	if err != nil {
		c.Ui.Error(fmt.Sprintf("Error getting Terraform Provider documentation directories: %s", err))
		return 1
	}

	if len(directories) == 0 {
		if config.Path == "" {
			c.Ui.Error("No Terraform Provider documentation directories found in current path")
		} else {
			c.Ui.Error(fmt.Sprintf("No Terraform Provider documentation directories found in path: %s", config.Path))
		}

		return 1
	}

	var allowedGuideSubcategories []string
	if v := config.AllowedGuideSubcategories; v != "" {
		allowedGuideSubcategories = strings.Split(v, ",")
	}

	if v := config.AllowedGuideSubcategoriesFile; v != "" {
		var err error
		allowedGuideSubcategories, err = allowedSubcategoriesFile(v)

		if err != nil {
			c.Ui.Error(fmt.Sprintf("Error getting allowed guide subcategories: %s", err))
			return 1
		}
	}

	var allowedResourceSubcategories []string
	if v := config.AllowedResourceSubcategories; v != "" {
		allowedResourceSubcategories = strings.Split(v, ",")
	}

	if v := config.AllowedResourceSubcategoriesFile; v != "" {
		var err error
		allowedResourceSubcategories, err = allowedSubcategoriesFile(v)

		if err != nil {
			c.Ui.Error(fmt.Sprintf("Error getting allowed resource subcategories: %s", err))
			return 1
		}
	}

	var ignoreFileMismatchDataSources []string
	if v := config.IgnoreFileMismatchDataSources; v != "" {
		ignoreFileMismatchDataSources = strings.Split(v, ",")
	}

	var ignoreFileMismatchResources []string
	if v := config.IgnoreFileMismatchResources; v != "" {
		ignoreFileMismatchResources = strings.Split(v, ",")
	}

	var ignoreFileMissingDataSources []string
	if v := config.IgnoreFileMissingDataSources; v != "" {
		ignoreFileMissingDataSources = strings.Split(v, ",")
	}

	var ignoreFileMissingResources []string
	if v := config.IgnoreFileMissingResources; v != "" {
		ignoreFileMissingResources = strings.Split(v, ",")
	}

	var schemaDataSources, schemaResources map[string]*tfjson.Schema
	if config.ProvidersSchemaJson != "" {
		ps, err := providerSchemas(config.ProvidersSchemaJson)

		if err != nil {
			c.Ui.Error(fmt.Sprintf("Error enabling Terraform Provider schema checks: %s", err))
			return 1
		}

		if config.ProviderName == "" {
			msg := `Unknown provider name for enabling Terraform Provider schema checks.

Check that the current working directory or provided path is prefixed with terraform-provider-*.`
			c.Ui.Error(msg)
			return 1
		}

		schemaDataSources = providerSchemasDataSources(ps, config.ProviderName, config.ProviderSource)
		schemaResources = providerSchemasResources(ps, config.ProviderName, config.ProviderSource)
	}

	fileOpts := &check.FileOptions{
		BasePath: config.Path,
	}
	checkOpts := &check.CheckOptions{
		DataSourceFileMismatch: &check.FileMismatchOptions{
			IgnoreFileMismatch: ignoreFileMismatchDataSources,
			IgnoreFileMissing:  ignoreFileMissingDataSources,
			ProviderName:       config.ProviderName,
			ResourceType:       check.ResourceTypeDataSource,
			Schemas:            schemaDataSources,
		},
		LegacyDataSourceFile: &check.LegacyDataSourceFileOptions{
			FileOptions: fileOpts,
			FrontMatter: &check.FrontMatterOptions{
				AllowedSubcategories: allowedResourceSubcategories,
				RequireSubcategory:   config.RequireResourceSubcategory,
			},
		},
		LegacyGuideFile: &check.LegacyGuideFileOptions{
			FileOptions: fileOpts,
			FrontMatter: &check.FrontMatterOptions{
				AllowedSubcategories: allowedGuideSubcategories,
				RequireSubcategory:   config.RequireGuideSubcategory,
			},
		},
		LegacyIndexFile: &check.LegacyIndexFileOptions{
			FileOptions: fileOpts,
		},
		LegacyResourceFile: &check.LegacyResourceFileOptions{
			Contents: &check.ContentsOptions{
				Enable:                config.EnableContentsCheck,
				RequireSchemaOrdering: config.RequireSchemaOrdering,
			},
			FileOptions: fileOpts,
			FrontMatter: &check.FrontMatterOptions{
				AllowedSubcategories: allowedResourceSubcategories,
				RequireSubcategory:   config.RequireResourceSubcategory,
			},
			ProviderName: config.ProviderName,
		},
		ProviderName:   config.ProviderName,
		ProviderSource: config.ProviderSource,
		RegistryDataSourceFile: &check.RegistryDataSourceFileOptions{
			FileOptions: fileOpts,
			FrontMatter: &check.FrontMatterOptions{
				AllowedSubcategories: allowedResourceSubcategories,
				RequireSubcategory:   config.RequireResourceSubcategory,
			},
		},
		RegistryGuideFile: &check.RegistryGuideFileOptions{
			FileOptions: fileOpts,
			FrontMatter: &check.FrontMatterOptions{
				AllowedSubcategories: allowedGuideSubcategories,
				RequireSubcategory:   config.RequireGuideSubcategory,
			},
		},
		RegistryIndexFile: &check.RegistryIndexFileOptions{
			FileOptions: fileOpts,
		},
		RegistryResourceFile: &check.RegistryResourceFileOptions{
			Contents: &check.ContentsOptions{
				Enable:                config.EnableContentsCheck,
				RequireSchemaOrdering: config.RequireSchemaOrdering,
			},
			FileOptions: fileOpts,
			FrontMatter: &check.FrontMatterOptions{
				AllowedSubcategories: allowedResourceSubcategories,
				RequireSubcategory:   config.RequireResourceSubcategory,
			},
			ProviderName: config.ProviderName,
		},
		ResourceFileMismatch: &check.FileMismatchOptions{
			IgnoreFileMismatch: ignoreFileMismatchResources,
			IgnoreFileMissing:  ignoreFileMissingResources,
			ProviderName:       config.ProviderName,
			ResourceType:       check.ResourceTypeResource,
			Schemas:            schemaResources,
		},
		IgnoreCdktfMissingFiles: config.IgnoreCdktfMissingFiles,
	}

	if err := check.NewCheck(checkOpts).Run(directories); err != nil {
		c.Ui.Error(fmt.Sprintf("Error checking Terraform Provider documentation: %s", err))
		return 1
	}

	return 0
}

func (c *CheckCommand) Synopsis() string {
	return "Checks Terraform Provider documentation"
}

func allowedSubcategoriesFile(path string) ([]string, error) {
	log.Printf("[DEBUG] Loading allowed subcategories file: %s", path)

	file, err := os.Open(path)

	if err != nil {
		return nil, fmt.Errorf("error opening allowed subcategories file (%s): %w", path, err)
	}

	defer file.Close()
	scanner := bufio.NewScanner(file)
	var allowedSubcategories []string

	for scanner.Scan() {
		allowedSubcategories = append(allowedSubcategories, scanner.Text())
	}

	if err != nil {
		return nil, fmt.Errorf("error reading allowed subcategories file (%s): %w", path, err)
	}

	return allowedSubcategories, nil
}

func providerNameFromCurrentDirectory() string {
	path, _ := os.Getwd()

	return providerNameFromPath(path)
}

func providerNameFromPath(path string) string {
	base := filepath.Base(path)

	if strings.ContainsAny(base, "./") {
		return ""
	}

	if !strings.HasPrefix(base, "terraform-provider-") {
		return ""
	}

	return strings.TrimPrefix(base, "terraform-provider-")
}

// providerSchemas reads, parses, and validates a provided terraform provider schema -json path.
func providerSchemas(path string) (*tfjson.ProviderSchemas, error) {
	log.Printf("[DEBUG] Loading providers schema JSON file: %s", path)

	content, err := os.ReadFile(path)

	if err != nil {
		return nil, fmt.Errorf("error reading providers schema JSON file (%s): %w", path, err)
	}

	var ps tfjson.ProviderSchemas

	if err := json.Unmarshal(content, &ps); err != nil {
		return nil, fmt.Errorf("error parsing providers schema JSON file (%s): %w", path, err)
	}

	if err := ps.Validate(); err != nil {
		return nil, fmt.Errorf("error validating providers schema JSON file (%s): %w", path, err)
	}

	return &ps, nil
}

// providerSchemasDataSources returns all data sources from a terraform providers schema -json provider.
func providerSchemasDataSources(ps *tfjson.ProviderSchemas, providerName string, providerSource string) map[string]*tfjson.Schema {
	if ps == nil || ps.Schemas == nil {
		return nil
	}

	provider, ok := ps.Schemas[providerSource]

	if !ok {
		provider, ok = ps.Schemas[providerName]
	}

	if !ok {
		log.Printf("[WARN] Provider source (%s) and name (%s) not found in provider schema", providerSource, providerName)
		return nil
	}

	dataSources := make([]string, 0, len(provider.DataSourceSchemas))

	for name := range provider.DataSourceSchemas {
		dataSources = append(dataSources, name)
	}

	sort.Strings(dataSources)

	log.Printf("[DEBUG] Found provider schema data sources: %v", dataSources)

	return provider.DataSourceSchemas
}

// providerSchemasResources returns all resources from a terraform providers schema -json provider.
func providerSchemasResources(ps *tfjson.ProviderSchemas, providerName string, providerSource string) map[string]*tfjson.Schema {
	if ps == nil || ps.Schemas == nil {
		return nil
	}

	provider, ok := ps.Schemas[providerSource]

	if !ok {
		provider, ok = ps.Schemas[providerName]
	}

	if !ok {
		log.Printf("[WARN] Provider source (%s) and name (%s) not found in provider schema", providerSource, providerName)
		return nil
	}

	resources := make([]string, 0, len(provider.ResourceSchemas))

	for name := range provider.ResourceSchemas {
		resources = append(resources, name)
	}

	sort.Strings(resources)

	log.Printf("[DEBUG] Found provider schema data sources: %v", resources)

	return provider.ResourceSchemas
}
