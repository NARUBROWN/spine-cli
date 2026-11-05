package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type platformBinding struct {
	Descriptor *platformDescriptor
	Prefix     string
}

func validatorEnvPrefix(provider string) string {
	return "SPINE_VALIDATOR_" + strings.ToUpper(providerIdentifier(provider)) + "_"
}

func platformBindings(options newProjectOptions) []platformBinding {
	var bindings []platformBinding
	for _, spec := range options.Events {
		if descriptor := eventDescriptor(spec); descriptor != nil {
			bindings = append(bindings, platformBinding{descriptor, eventEnvPrefix(spec.Name)})
		}
	}
	if descriptor := selectedValidatorDescriptor(options); descriptor != nil && descriptor.ConfigType != "" {
		bindings = append(bindings, platformBinding{descriptor, validatorEnvPrefix(options.Validator)})
	}
	return bindings
}

func renderDescriptorConfig(d *platformDescriptor) string {
	alias := descriptorAlias(d)
	var source strings.Builder
	fmt.Fprintf(&source, "func %s(prefix string) (%s.%s,error) {\nconfig := %s.%s{}\n", descriptorLoader(d), alias, d.ConfigType, alias, d.ConfigType)
	for _, field := range d.Fields {
		if literal, _ := configLiteral(field.Type, field.Default); literal != "" {
			fmt.Fprintf(&source, "config.%s = %s\n", field.Field, literal)
		}
		if field.Env == "" {
			continue
		}
		fmt.Fprintf(&source, "{\nkey := prefix+%q\nvalue := strings.TrimSpace(os.Getenv(key))\n", field.Env)
		if field.Required {
			source.WriteString("if value == \"\" {return config,fmt.Errorf(\"%s is required\",key)}\n")
		}
		source.WriteString("if value != \"\" {\n")
		switch field.Type {
		case "string":
			fmt.Fprintf(&source, "config.%s = value\n", field.Field)
		case "strings":
			source.WriteString("values := []string{}\nfor _,item := range strings.Split(value,\",\") {\nitem = strings.TrimSpace(item)\nif item == \"\" {return config,fmt.Errorf(\"%s contains an empty item\",key)}\nvalues = append(values,item)\n}\n")
			fmt.Fprintf(&source, "config.%s = values\n", field.Field)
		case "bool", "int", "duration":
			parser := map[string]string{"bool": "strconv.ParseBool", "int": "strconv.Atoi", "duration": "time.ParseDuration"}[field.Type]
			fmt.Fprintf(&source, "parsed,err := %s(value)\nif err != nil {return config,fmt.Errorf(\"%%s must be a valid %s\",key)}\nconfig.%s = parsed\n", parser, field.Type, field.Field)
		}
		source.WriteString("}\n}\n")
	}
	source.WriteString("return config,nil\n}\n")
	return source.String()
}

func renderPlatformConfig(options newProjectOptions) string {
	imports := map[string]string{}
	descriptors := map[string]*platformDescriptor{}
	needsTime := false
	for _, binding := range platformBindings(options) {
		d := binding.Descriptor
		module := providerModule(d.Provider)
		if d.Kind == "validator" {
			module = validatorModule(d.Provider)
		}
		imports[descriptorAlias(d)] = module
		descriptors[module] = d
		for _, field := range d.Fields {
			needsTime = needsTime || (field.Type == "duration" && field.Env != "")
		}
	}
	var source strings.Builder
	source.WriteString("package application\nimport (\n\"fmt\"\n\"os\"\n\"strconv\"\n\"strings\"\n")
	if needsTime {
		source.WriteString("\"time\"\n")
	}
	aliases := make([]string, 0, len(imports))
	for alias := range imports {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		fmt.Fprintf(&source, "%s %q\n", alias, imports[alias])
	}
	source.WriteString(")\n" + generatedConfigHelpers)
	modules := make([]string, 0, len(descriptors))
	for module := range descriptors {
		modules = append(modules, module)
	}
	sort.Strings(modules)
	for _, module := range modules {
		source.WriteString(renderDescriptorConfig(descriptors[module]))
	}
	return source.String()
}

func exampleEnv(field platformConfigField) (string, bool) {
	if len(field.Example) == 0 {
		return "", false
	}
	if field.Type == "string" || field.Type == "duration" {
		var value string
		_ = json.Unmarshal(field.Example, &value)
		return value, true
	}
	if field.Type == "strings" {
		var values []string
		_ = json.Unmarshal(field.Example, &values)
		return strings.Join(values, ","), true
	}
	return string(field.Example), true
}

func descriptorTestSetup(options newProjectOptions) string {
	var source strings.Builder
	for _, binding := range platformBindings(options) {
		for _, field := range binding.Descriptor.Fields {
			if value, ok := exampleEnv(field); ok && field.Env != "" {
				fmt.Fprintf(&source, "t.Setenv(%q,%q)\n", binding.Prefix+field.Env, value)
			}
		}
	}
	return source.String()
}

func renderPlatformConfigTest(options newProjectOptions) string {
	var source strings.Builder
	source.WriteString("package application\nimport \"testing\"\nfunc TestPlatformConfiguration(t *testing.T) {\n" + descriptorTestSetup(options))
	source.WriteString("app,options,err := BuildApplication()\nif err != nil {t.Fatal(err)}\nif err := app.Validate(options); err != nil {t.Fatal(err)}\n")
	for _, binding := range platformBindings(options) {
		for _, field := range binding.Descriptor.Fields {
			value, hasExample := exampleEnv(field)
			if field.Env == "" || !hasExample {
				continue
			}
			invalid := ""
			if field.Type == "bool" || field.Type == "int" || field.Type == "duration" {
				invalid = "invalid"
			} else if !field.Required {
				continue
			}
			key := binding.Prefix + field.Env
			fmt.Fprintf(&source, "t.Setenv(%q,%q)\nif app,_,err := BuildApplication(); err == nil || app != nil {t.Fatal(\"invalid platform configuration was accepted\")}\nt.Setenv(%q,%q)\n", key, invalid, key, value)
		}
	}
	source.WriteString("}\n")
	return source.String()
}

func descriptorReadmeFields(d *platformDescriptor, prefix string) string {
	var required, optional []string
	for _, field := range d.Fields {
		if field.Env == "" {
			continue
		}
		key := "`" + prefix + field.Env + "`"
		if field.Required {
			required = append(required, key)
		} else {
			optional = append(optional, key)
		}
	}
	return "필수 " + strings.Join(required, ", ") + "; 선택 " + strings.Join(optional, ", ") + "."
}

func descriptorFingerprints(options newProjectOptions) map[string]string {
	result := map[string]string{}
	for _, spec := range options.Events {
		if d := eventDescriptor(spec); d != nil {
			result[providerModule(spec.Provider)] = d.SHA256
		}
	}
	if d := selectedValidatorDescriptor(options); d != nil {
		result[validatorModule(options.Validator)] = d.SHA256
	}
	return result
}
