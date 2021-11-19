// Copyright 2020 ConsenSys Software Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated by gnark DO NOT EDIT

package cs

import (
	"fmt"
	"io"
	"strings"

	"github.com/consensys/gnark/internal/backend/compiled"
	"text/template"

	"github.com/consensys/gnark-crypto/ecc/bls12-377/fr"
)

// ToHTML returns an HTML human-readable representation of the constraint system
func (cs *R1CS) ToHTML(w io.Writer) error {
	t, err := template.New("cs.html").Funcs(template.FuncMap{
		"formatTerm":             formatTerm,
		"formatLinearExpression": formatLinearExpression,
		"add":                    add,
		"sub":                    sub,
	}).Parse(compiled.R1CSTemplate)
	if err != nil {
		return err
	}

	return t.Execute(w, cs)
}

// ToHTML returns an HTML human-readable representation of the constraint system
func (cs *SparseR1CS) ToHTML(w io.Writer) error {
	t, err := template.New("scs.html").Funcs(template.FuncMap{
		"formatTerm":  formatTerm,
		"formatCoeff": formatCoeff,
		"add":         add,
		"sub":         sub,
	}).Parse(compiled.SparseR1CSTemplate)
	if err != nil {
		return err
	}

	return t.Execute(w, cs)
}

func formatCoeff(cID int, coeffs []fr.Element) string {
	if cID == compiled.CoeffIdMinusOne {
		// print neg sign
		return "<span class=\"coefficient\">-1</span>"
	}
	var sbb strings.Builder
	sbb.WriteString("<span class=\"coefficient\">")
	sbb.WriteString(coeffs[cID].String())
	sbb.WriteString("</span>")
	return sbb.String()
}

func formatLinearExpression(l compiled.LinearExpression, cs compiled.CS, coeffs []fr.Element) string {
	var sbb strings.Builder
	for i := 0; i < len(l); i++ {
		sbb.WriteString(formatTerm(l[i], cs, coeffs, false))
		if i+1 < len(l) {
			sbb.WriteString(" + ")
		}
	}
	return sbb.String()
}

func formatTerm(t compiled.Term, cs compiled.CS, coeffs []fr.Element, offset bool) string {
	var sbb strings.Builder

	tID := t.CoeffID()
	if tID == compiled.CoeffIdOne {
		// do nothing, just print the variable
	} else if tID == compiled.CoeffIdMinusOne {
		// print neg sign
		sbb.WriteString("<span class=\"coefficient\">-</span>")
	} else if tID == compiled.CoeffIdZero {
		return "<span class=\"coefficient\">0</span>"
	} else {
		sbb.WriteString("<span class=\"coefficient\">")
		sbb.WriteString(coeffs[tID].String())
		sbb.WriteString("</span>*")
	}

	vID := t.VariableID()
	class := ""
	switch t.VariableVisibility() {
	case compiled.Internal:
		class = "internal"
		if _, ok := cs.MHints[vID]; ok {
			class = "hint"
		}
	case compiled.Public:
		class = "public"
	case compiled.Secret:
		class = "secret"
	case compiled.Virtual:
		class = "virtual"
	case compiled.Unset:
		class = "unset"
	default:
		panic("not implemented")
	}

	if t.VariableVisibility() == compiled.Secret {
		vID -= cs.NbPublicVariables
		sbb.WriteString(fmt.Sprintf("<span class=\"%s\">%s</span>", class, cs.SecretNames[vID]))
	} else if t.VariableVisibility() == compiled.Public {
		sbb.WriteString(fmt.Sprintf("<span class=\"%s\">%s</span>", class, cs.PublicNames[vID]))
	} else {
		if offset {
			vID++ // for sparse R1CS, we offset to have same variable numbers as in R1CS
		}
		sbb.WriteString(fmt.Sprintf("<span class=\"%s\">v%d</span>", class, vID))
	}

	return sbb.String()

}

func add(a, b int) int {
	return a + b
}

func sub(a, b int) int {
	return a - b
}
