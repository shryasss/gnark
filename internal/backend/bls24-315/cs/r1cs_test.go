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

package cs_test

import (
	"bytes"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/internal/backend/circuits"
	"reflect"
	"testing"

	"github.com/consensys/gnark/internal/backend/bls24-315/cs"
)

func TestSerialization(t *testing.T) {

	var buffer, buffer2 bytes.Buffer

	for name, circuit := range circuits.Circuits {

		r1cs, err := frontend.Compile(ecc.BLS24_315, backend.GROTH16, circuit.Circuit)
		if err != nil {
			t.Fatal(err)
		}
		if testing.Short() && r1cs.GetNbConstraints() > 50 {
			continue
		}

		// copmpile a second time to ensure determinism
		r1cs2, err := frontend.Compile(ecc.BLS24_315, backend.GROTH16, circuit.Circuit)
		if err != nil {
			t.Fatal(err)
		}

		// no need to serialize.
		r1cs.SetLoggerOutput(nil)
		r1cs2.SetLoggerOutput(nil)
		{
			buffer.Reset()
			t.Log(name)
			var err error
			var written, read int64
			written, err = r1cs.WriteTo(&buffer)
			if err != nil {
				t.Fatal(err)
			}
			var reconstructed cs.R1CS
			read, err = reconstructed.ReadFrom(&buffer)
			if err != nil {
				t.Fatal(err)
			}
			if written != read {
				t.Fatal("didn't read same number of bytes we wrote")
			}
			// compare original and reconstructed
			if !reflect.DeepEqual(r1cs, &reconstructed) {
				t.Fatal("round trip serialization failed")
			}
		}

		// ensure determinism in compilation / serialization / reconstruction
		{
			buffer.Reset()
			n, err := r1cs.WriteTo(&buffer)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				t.Fatal("No bytes are written")
			}

			buffer2.Reset()
			_, err = r1cs2.WriteTo(&buffer2)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(buffer.Bytes(), buffer2.Bytes()) {
				t.Fatal("compilation of R1CS is not deterministic")
			}

			var r, r2 cs.R1CS
			n, err = r.ReadFrom(&buffer)
			if err != nil {
				t.Fatal(nil)
			}
			if n == 0 {
				t.Fatal("No bytes are read")
			}
			_, err = r2.ReadFrom(&buffer2)
			if err != nil {
				t.Fatal(nil)
			}

			if !reflect.DeepEqual(r, r2) {
				t.Fatal("compilation of R1CS is not deterministic (reconstruction)")
			}
		}
	}
}