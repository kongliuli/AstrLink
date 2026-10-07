package hunyuanapi

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
)

func validateInstance(schemaRaw, instanceRaw json.RawMessage) error {
	var schema map[string]any
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		return fmt.Errorf("response schema is not an object")
	}
	if err := checkSchema(schema); err != nil {
		return err
	}
	var instance any
	if err := json.Unmarshal(instanceRaw, &instance); err != nil {
		return fmt.Errorf("model output is not JSON")
	}
	return matchSchema(schema, instance)
}

func checkSchema(schema map[string]any) error {
	if schema == nil {
		return fmt.Errorf("schema must be an object")
	}
	for key, value := range schema {
		switch key {
		case "title", "description":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%s must be a string", key)
			}
		case "type":
			name, ok := value.(string)
			if !ok || !stringsIn(name, "object", "array", "string", "number", "integer", "boolean", "null") {
				return fmt.Errorf("unsupported schema type")
			}
		case "properties":
			props, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("properties must be an object")
			}
			for _, child := range props {
				nested, ok := child.(map[string]any)
				if !ok {
					return fmt.Errorf("property schema must be an object")
				}
				if err := checkSchema(nested); err != nil {
					return err
				}
			}
		case "items":
			nested, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("items must be a schema object")
			}
			if err := checkSchema(nested); err != nil {
				return err
			}
		case "required":
			names, ok := value.([]any)
			if !ok {
				return fmt.Errorf("required must be an array")
			}
			for _, name := range names {
				if _, ok := name.(string); !ok {
					return fmt.Errorf("required entries must be strings")
				}
			}
		case "additionalProperties":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("additionalProperties must be boolean")
			}
		case "enum":
			values, ok := value.([]any)
			if !ok || len(values) == 0 {
				return fmt.Errorf("enum must be a nonempty array")
			}
		case "const":
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum":
			if _, ok := value.(float64); !ok {
				return fmt.Errorf("%s must be a number", key)
			}
		default:
			return fmt.Errorf("unsupported schema keyword %s", key)
		}
	}
	return nil
}

func stringsIn(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func matchSchema(schema map[string]any, instance any) error {
	if values, ok := schema["enum"].([]any); ok {
		found := false
		for _, value := range values {
			if reflect.DeepEqual(value, instance) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("json does not match enum")
		}
	}
	if value, exists := schema["const"]; exists && !reflect.DeepEqual(value, instance) {
		return fmt.Errorf("json does not match const")
	}
	if number, ok := instance.(float64); ok {
		for _, key := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"} {
			limit, exists := schema[key].(float64)
			if exists && ((key == "minimum" && number < limit) || (key == "maximum" && number > limit) || (key == "exclusiveMinimum" && number <= limit) || (key == "exclusiveMaximum" && number >= limit)) {
				return fmt.Errorf("json violates %s", key)
			}
		}
	}
	if values, ok := instance.([]any); ok {
		if child, ok := schema["items"].(map[string]any); ok {
			for _, value := range values {
				if err := matchSchema(child, value); err != nil {
					return err
				}
			}
		}
	}
	typeName, _ := schema["type"].(string)
	if typeName != "" && !jsonTypeOK(typeName, instance) {
		return fmt.Errorf("json does not match schema type %s", typeName)
	}
	if object, ok := instance.(map[string]any); ok {
		props, _ := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]any); ok {
			for _, item := range required {
				name, _ := item.(string)
				if _, exists := object[name]; !exists {
					return fmt.Errorf("missing required property %s", name)
				}
			}
		}
		if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
			for key := range object {
				if _, exists := props[key]; !exists {
					return fmt.Errorf("unexpected property %s", key)
				}
			}
		}
		for key, spec := range props {
			child, exists := object[key]
			if !exists {
				continue
			}
			childSchema, _ := spec.(map[string]any)
			if err := matchSchema(childSchema, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func jsonTypeOK(want string, instance any) bool {
	switch want {
	case "object":
		_, ok := instance.(map[string]any)
		return ok
	case "string":
		_, ok := instance.(string)
		return ok
	case "number":
		_, ok := instance.(float64)
		return ok
	case "integer":
		number, ok := instance.(float64)
		return ok && number == math.Trunc(number)
	case "null":
		return instance == nil
	case "boolean":
		_, ok := instance.(bool)
		return ok
	case "array":
		_, ok := instance.([]any)
		return ok
	default:
		return false
	}
}
