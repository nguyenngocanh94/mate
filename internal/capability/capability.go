// Package capability is the vocabulary a registry's contract declares its
// optional parts in: a Cap per capability, with a named status and what it
// rests on. The harness registry (internal/harness) and the tool registry
// (internal/tool) share it, and so share one contract rule, Check
// (docs/plans/workspace-layout-and-tools-2026-10-08.md, section 5.2).
package capability

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// CapStatus is what is known about one capability of one harness or tool.
// The words are the ones the TUI probe plan uses
// (tui-probe-redesign-2026-09-27.md, section 5.1), so the plans share one
// vocabulary.
type CapStatus string

const (
	// Undeclared is the zero value: the profile never answered. It is
	// always a bug.
	Undeclared CapStatus = ""
	// Verified means measured on a named version; Impl is set.
	Verified CapStatus = "verified"
	// Unsupported means the profile does not have it; Reason says why.
	Unsupported CapStatus = "unsupported"
	// Unknown means not measured yet; Reason says what is missing.
	Unknown CapStatus = "unknown"
)

// Cap is one capability's declaration. Impl is set exactly when Status is
// Verified: code still being measured belongs in tests and labs, not
// under an unknown capability.
type Cap[T any] struct {
	Status   CapStatus
	Impl     T
	Evidence Evidence
	Reason   string
}

// Verified reports whether the capability can be used.
func (c Cap[T]) Verified() bool { return c.Status == Verified }

// Declaration is the Cap with its implementation erased, under the name of
// the field that holds it. Name is empty until Declarations fills it.
func (c Cap[T]) Declaration() Declaration {
	return Declaration{Status: c.Status, HasImpl: any(c.Impl) != nil, Evidence: c.Evidence, Reason: c.Reason}
}

// Evidence is what a verified capability rests on.
type Evidence struct {
	// Version is the harness or tool version it was measured on.
	Version string
	// Measured is when.
	Measured string
	// Proof names the test, capture or record that shows it.
	Proof string
}

// Declaration is one capability as a contract suite reads it.
type Declaration struct {
	// Name is the field of the capabilities struct that holds it.
	Name     string
	Status   CapStatus
	HasImpl  bool
	Evidence Evidence
	Reason   string
}

// Checkable is a Cap of any type.
type Checkable interface {
	Declaration() Declaration
}

// Declarations reads every field of a capabilities struct (harness.
// Capabilities, tool.Capabilities) by reflection, so a field added to a
// contract is read without anyone listing it. Every field must be a Cap
// over an interface: Impl is set exactly when it is not nil.
func Declarations(set any) ([]Declaration, error) {
	v := reflect.ValueOf(set)
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("capabilities: a %T is not a struct of Cap fields", set)
	}
	if v.NumField() == 0 {
		return nil, fmt.Errorf("capabilities: %s has no fields", v.Type())
	}
	out := make([]Declaration, 0, v.NumField())
	for i := 0; i < v.NumField(); i++ {
		field, f := v.Type().Field(i), v.Field(i)
		var c Checkable
		ok := field.IsExported() && f.Kind() == reflect.Struct
		if ok {
			c, ok = f.Interface().(Checkable)
		}
		if !ok || f.FieldByName("Impl").Kind() != reflect.Interface {
			return nil, fmt.Errorf("capabilities: %s.%s is not a Cap[T] over an interface", v.Type(), field.Name)
		}
		d := c.Declaration()
		d.Name = field.Name
		out = append(out, d)
	}
	return out, nil
}

// Check holds every capability of a capabilities struct to the contract:
// none is undeclared; Impl is set exactly when verified; verified rests on
// full evidence, unsupported and unknown on a reason. It returns every
// refusal, each naming its field, or nil.
func Check(set any) error {
	decls, err := Declarations(set)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range decls {
		switch d.Status {
		case Undeclared:
			errs = append(errs, fmt.Errorf("%s is undeclared: every profile answers every capability", d.Name))
			continue
		case Verified, Unsupported, Unknown:
		default:
			errs = append(errs, fmt.Errorf("%s has status %q, not one of verified, unsupported, unknown", d.Name, d.Status))
			continue
		}
		verified := d.Status == Verified
		if d.HasImpl != verified {
			errs = append(errs, fmt.Errorf("%s is %s with Impl set = %v: Impl is set exactly when verified", d.Name, d.Status, d.HasImpl))
		}
		if verified {
			if d.Evidence.Version == "" || d.Evidence.Measured == "" || d.Evidence.Proof == "" {
				errs = append(errs, fmt.Errorf("%s is verified without full evidence (version, when, proof): %+v", d.Name, d.Evidence))
			}
		} else if strings.TrimSpace(d.Reason) == "" {
			errs = append(errs, fmt.Errorf("%s is %s without a reason", d.Name, d.Status))
		}
	}
	return errors.Join(errs...)
}
