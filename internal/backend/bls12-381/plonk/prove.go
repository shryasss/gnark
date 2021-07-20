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

package plonk

import (
	"math/big"
	"sync"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr/polynomial"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr/kzg"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr/fft"

	bls12_381witness "github.com/consensys/gnark/internal/backend/bls12-381/witness"

	"github.com/consensys/gnark/internal/backend/bls12-381/cs"

	"github.com/consensys/gnark-crypto/fiat-shamir"
	"github.com/consensys/gnark/internal/utils"
)

type Proof struct {

	// Commitments to the solution vectors
	LRO [3]kzg.Digest

	// Commitment to Z, the permutation polynomial
	Z kzg.Digest

	// Commitments to h1, h2, h3 such that h = h1 + Xh2 + X**2h3 is the quotient polynomial
	H [3]kzg.Digest

	// Batch opening proof of h1 + zeta*h2 + zeta**2h3, linearizedPolynomial, l, r, o, s1, s2
	BatchedProof kzg.BatchOpeningProof

	// Opening proof of Z at zeta*mu
	ZShiftedOpening kzg.OpeningProof
}

// Prove from the public data
func Prove(spr *cs.SparseR1CS, pk *ProvingKey, fullWitness bls12_381witness.Witness) (*Proof, error) {

	// create a transcript manager to apply Fiat Shamir
	fs := fiatshamir.NewTranscript(fiatshamir.SHA256, "gamma", "alpha", "zeta")

	// result
	proof := &Proof{}

	// compute the solution
	solution, err := spr.Solve(fullWitness)
	if err != nil {
		return nil, err
	}

	// query l, r, o in Lagrange basis
	ll, lr, lo := computeLRO(spr, pk, solution)

	// save ll, lr, lo, and make a copy of them in canonical basis.
	// We commit them and derive gamma from them.
	sizeCommon := int64(pk.DomainNum.Cardinality)
	cl := make(polynomial.Polynomial, sizeCommon)
	cr := make(polynomial.Polynomial, sizeCommon)
	co := make(polynomial.Polynomial, sizeCommon)
	copy(cl, ll)
	copy(cr, lr)
	copy(co, lo)
	pk.DomainNum.FFTInverse(cl, fft.DIF, 0)
	pk.DomainNum.FFTInverse(cr, fft.DIF, 0)
	pk.DomainNum.FFTInverse(co, fft.DIF, 0)
	{
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			fft.BitReverse(cl)
			wg.Done()
		}()
		go func() {
			fft.BitReverse(cr)
			wg.Done()
		}()
		fft.BitReverse(co)
		wg.Wait()
	}

	// derive gamma from the Comm(l), Comm(r), Comm(o)
	if proof.LRO[0], err = kzg.Commit(cl, pk.Vk.KZGSRS); err != nil {
		return nil, err
	}
	if proof.LRO[1], err = kzg.Commit(cr, pk.Vk.KZGSRS); err != nil {
		return nil, err
	}
	if proof.LRO[2], err = kzg.Commit(co, pk.Vk.KZGSRS); err != nil {
		return nil, err
	}
	if err = fs.Bind("gamma", proof.LRO[0].Marshal()); err != nil {
		return nil, err
	}
	if err = fs.Bind("gamma", proof.LRO[1].Marshal()); err != nil {
		return nil, err
	}
	if err = fs.Bind("gamma", proof.LRO[2].Marshal()); err != nil {
		return nil, err
	}
	bgamma, err := fs.ComputeChallenge("gamma")
	if err != nil {
		return nil, err
	}
	var gamma fr.Element
	gamma.SetBytes(bgamma)

	chZ := make(chan struct{}, 1)
	var z, zu polynomial.Polynomial
	go func() {
		// compute Z, the permutation accumulator polynomial, in Lagrange basis
		z = computeZ(ll, lr, lo, pk, gamma)

		// compute Z(uX), in Lagrange basis
		zu = shiftZ(z)
		close(chZ)
	}()

	// compute the evaluations of l, r, o on odd cosets of (Z/8mZ)/(Z/mZ)
	evalL := make([]fr.Element, 4*pk.DomainNum.Cardinality)
	evalR := make([]fr.Element, 4*pk.DomainNum.Cardinality)
	evalO := make([]fr.Element, 4*pk.DomainNum.Cardinality)
	evaluateCosets(cl, evalL, &pk.DomainNum)
	evaluateCosets(cr, evalR, &pk.DomainNum)
	evaluateCosets(co, evalO, &pk.DomainNum)

	<-chZ

	// compute qk in canonical basis, completed with the public inputs
	qkFullC := make(polynomial.Polynomial, sizeCommon)
	copy(qkFullC, fullWitness[:spr.NbPublicVariables])
	copy(qkFullC[spr.NbPublicVariables:], pk.LQk[spr.NbPublicVariables:])
	pk.DomainNum.FFTInverse(qkFullC, fft.DIF, 0)
	fft.BitReverse(qkFullC)

	// compute the evaluation of qlL+qrR+qmL.R+qoO+k on the odd cosets of (Z/8mZ)/(Z/mZ)
	constraintsInd := evalConstraints(pk, evalL, evalR, evalO, qkFullC)

	// put back z, zu in canonical basis
	pk.DomainNum.FFTInverse(z, fft.DIF, 0)
	pk.DomainNum.FFTInverse(zu, fft.DIF, 0)
	fft.BitReverse(z)
	fft.BitReverse(zu)

	// evaluate z, zu on the odd cosets of (Z/8mZ)/(Z/mZ)
	evalZ := make([]fr.Element, 4*pk.DomainNum.Cardinality)
	evalZu := make([]fr.Element, 4*pk.DomainNum.Cardinality)
	evaluateCosets(z, evalZ, &pk.DomainNum)
	evaluateCosets(zu, evalZu, &pk.DomainNum)

	// compute zu*g1*g2*g3-z*f1*f2*f3 on the odd cosets of (Z/8mZ)/(Z/mZ)
	constraintsOrdering := evalConstraintOrdering(pk, evalZ, evalZu, evalL, evalR, evalO, gamma)

	// compute L1*(z-1) on the odd cosets of (Z/8mZ)/(Z/mZ)
	startsAtOne := evalStartsAtOne(pk, evalZ)

	// commit to Z
	if proof.Z, err = kzg.Commit(z, pk.Vk.KZGSRS); err != nil {
		return nil, err
	}

	// derive alpha from the Comm(l), Comm(r), Comm(o), Com(Z)
	if err = fs.Bind("alpha", proof.Z.Marshal()); err != nil {
		return nil, err
	}
	balpha, err := fs.ComputeChallenge("alpha")
	if err != nil {
		return nil, err
	}
	var alpha fr.Element
	alpha.SetBytes(balpha)

	// compute h in canonical form
	h1, h2, h3 := computeH(pk, constraintsInd, constraintsOrdering, startsAtOne, alpha)

	// commit to h (3 commitments h1 + x**n*h2 + x**2n*h3)
	if proof.H[0], err = kzg.Commit(h1, pk.Vk.KZGSRS); err != nil {
		return nil, err
	}
	if proof.H[1], err = kzg.Commit(h2, pk.Vk.KZGSRS); err != nil {
		return nil, err
	}
	if proof.H[2], err = kzg.Commit(h3, pk.Vk.KZGSRS); err != nil {
		return nil, err
	}

	// derive zeta, the point of evaluation
	if err = fs.Bind("zeta", proof.H[0].Marshal()); err != nil {
		return nil, err
	}
	if err = fs.Bind("zeta", proof.H[1].Marshal()); err != nil {
		return nil, err
	}
	if err = fs.Bind("zeta", proof.H[2].Marshal()); err != nil {
		return nil, err
	}
	bzeta, err := fs.ComputeChallenge("zeta")
	if err != nil {
		return nil, err
	}
	var zeta fr.Element
	zeta.SetBytes(bzeta)

	// open Z at zeta*z
	var zetaShifted fr.Element
	zetaShifted.Mul(&zeta, &pk.Vk.Generator)
	proof.ZShiftedOpening, _ = kzg.Open(
		z,
		&zetaShifted,
		&pk.DomainNum,
		pk.Vk.KZGSRS,
	)

	zuzeta := proof.ZShiftedOpening.ClaimedValue

	// compute evaluations of l, r, o, z at zeta
	lzeta := cl.Eval(&zeta)
	rzeta := cr.Eval(&zeta)
	ozeta := co.Eval(&zeta)

	// compute the linearization polynomial r at zeta (goal: save committing separately to z, ql, qr, qm, qo, k)
	linearizedPolynomial := computeLinearizedPolynomial(
		lzeta,
		rzeta,
		ozeta,
		alpha,
		gamma,
		zeta,
		zuzeta,
		z,
		pk,
	)

	// foldedHDigest = Comm(h1) + zeta**m*Comm(h2) + zeta**2m*Comm(h3)
	var bZetaPowerm big.Int
	sizeBigInt := big.NewInt(sizeCommon)
	var zetaPowerm fr.Element
	zetaPowerm.Exp(zeta, sizeBigInt)
	zetaPowerm.ToBigIntRegular(&bZetaPowerm)
	foldedHDigest := proof.H[2]
	foldedHDigest.ScalarMultiplication(&foldedHDigest, &bZetaPowerm)
	foldedHDigest.Add(&foldedHDigest, &proof.H[1])                   // zeta**m*Comm(h3)
	foldedHDigest.ScalarMultiplication(&foldedHDigest, &bZetaPowerm) // zeta**2m*Comm(h3) + zeta**m*Comm(h2)
	foldedHDigest.Add(&foldedHDigest, &proof.H[0])                   // zeta**2m*Comm(h3) + zeta**m*Comm(h2) + Comm(h1)

	// foldedH = h1 + zeta*h2 + zeta**2*h3
	foldedH := h3.Clone()
	foldedH.ScaleInPlace(&zetaPowerm) // zeta**m*h3
	foldedH.Add(foldedH, h2)          // zeta**m*h3+h2
	foldedH.ScaleInPlace(&zetaPowerm) // zeta**2m*h3+h2*zeta**m
	foldedH.Add(foldedH, h1)          // zeta**2m*h3+zeta**m*h2 + h1
	// foldedH correct

	// TODO @gbotrel @thomas check errors.

	// TODO this commitment is only necessary to derive the challenge, we should
	// be able to avoid doing it and get the challenge in another way
	linearizedPolynomialDigest, _ := kzg.Commit(linearizedPolynomial, pk.Vk.KZGSRS)

	// Batch open the first list of polynomials
	proof.BatchedProof, _ = kzg.BatchOpenSinglePoint(
		[]polynomial.Polynomial{
			foldedH,
			linearizedPolynomial,
			cl,
			cr,
			co,
			pk.CS1,
			pk.CS2,
		},
		[]kzg.Digest{
			foldedHDigest,
			linearizedPolynomialDigest,
			proof.LRO[0],
			proof.LRO[1],
			proof.LRO[2],
			pk.Vk.S[0],
			pk.Vk.S[1],
		},
		&zeta,
		&pk.DomainNum,
		pk.Vk.KZGSRS,
	)

	return proof, nil

}

// computeLRO extracts the solution l, r, o, and returns it in lagrange form.
// solution = [ public | secret | internal ]
func computeLRO(spr *cs.SparseR1CS, pk *ProvingKey, solution []fr.Element) (polynomial.Polynomial, polynomial.Polynomial, polynomial.Polynomial) {

	s := int(pk.DomainNum.Cardinality)

	var l, r, o polynomial.Polynomial
	l = make([]fr.Element, s)
	r = make([]fr.Element, s)
	o = make([]fr.Element, s)

	for i := 0; i < spr.NbPublicVariables; i++ { // placeholders
		l[i].Set(&solution[i])
		r[i].Set(&solution[0])
		o[i].Set(&solution[0])
	}
	offset := spr.NbPublicVariables
	for i := 0; i < len(spr.Constraints); i++ { // constraints
		l[offset+i].Set(&solution[spr.Constraints[i].L.VariableID()])
		r[offset+i].Set(&solution[spr.Constraints[i].R.VariableID()])
		o[offset+i].Set(&solution[spr.Constraints[i].O.VariableID()])
	}
	offset += len(spr.Constraints)
	for i := 0; i < len(spr.Assertions); i++ { // assertions
		l[offset+i].Set(&solution[spr.Assertions[i].L.VariableID()])
		r[offset+i].Set(&solution[spr.Assertions[i].R.VariableID()])
		o[offset+i].Set(&solution[spr.Assertions[i].O.VariableID()])
	}
	offset += len(spr.Assertions)
	for i := 0; i < s-offset; i++ { // offset to reach 2**n constraints (where the id of l,r,o is 0, so we assign solution[0])
		l[offset+i].Set(&solution[0])
		r[offset+i].Set(&solution[0])
		o[offset+i].Set(&solution[0])
	}

	return l, r, o

}

// computeZ computes Z (in Lagrange basis), where:
//
// * Z of degree n (domainNum.Cardinality)
// * Z(1)=1
// 								   (l_i+z**i+gamma)*(r_i+u*z**i+gamma)*(o_i+u**2z**i+gamma)
//	* for i>0: Z(u**i) = Pi_{k<i} -------------------------------------------------------
//								     (l_i+s1+gamma)*(r_i+s2+gamma)*(o_i+s3+gamma)
//
//	* l, r, o are the solution in Lagrange basis
func computeZ(l, r, o polynomial.Polynomial, pk *ProvingKey, gamma fr.Element) polynomial.Polynomial {

	z := make(polynomial.Polynomial, pk.DomainNum.Cardinality)
	nbElmts := int(pk.DomainNum.Cardinality)
	gInv := make(polynomial.Polynomial, pk.DomainNum.Cardinality)

	var f [3]fr.Element
	var g [3]fr.Element
	var u [3]fr.Element
	u[0].SetOne()
	u[1].Set(&pk.Vk.Shifter[0])
	u[2].Set(&pk.Vk.Shifter[1])

	z[0].SetOne()
	gInv[0].SetOne()

	for i := 0; i < nbElmts-1; i++ {

		f[0].Add(&l[i], &u[0]).Add(&f[0], &gamma) //l_i+z**i+gamma
		f[1].Add(&r[i], &u[1]).Add(&f[1], &gamma) //r_i+u*z**i+gamma
		f[2].Add(&o[i], &u[2]).Add(&f[2], &gamma) //o_i+u**2*z**i+gamma

		g[0].Add(&l[i], &pk.LS1[i]).Add(&g[0], &gamma) //l_i+z**i+gamma
		g[1].Add(&r[i], &pk.LS2[i]).Add(&g[1], &gamma) //r_i+u*z**i+gamma
		g[2].Add(&o[i], &pk.LS3[i]).Add(&g[2], &gamma) //o_i+u**2*z**i+gamma

		f[0].Mul(&f[0], &f[1]).Mul(&f[0], &f[2]) // (l_i+z**i+gamma)*(r_i+u*z**i+gamma)*(o_i+u**2z**i+gamma)
		g[0].Mul(&g[0], &g[1]).Mul(&g[0], &g[2]) //  (l_i+s1+gamma)*(r_i+s2+gamma)*(o_i+s3+gamma)

		gInv[i+1] = g[0]
		z[i+1].Mul(&z[i], &f[0]) //.Div(&z[i+1], &g[0]) --> use montgomery batch inversion in a second loop

		u[0].Mul(&u[0], &pk.DomainNum.Generator) // z**i -> z**i+1
		u[1].Mul(&u[1], &pk.DomainNum.Generator) // u*z**i -> u*z**i+1
		u[2].Mul(&u[2], &pk.DomainNum.Generator) // u**2*z**i -> u**2*z**i+1
	}

	//.Div(&z[i+1], &g[0])
	gInv = fr.BatchInvert(gInv)
	acc := fr.One()
	for i := 1; i < nbElmts; i++ {
		acc.Mul(&acc, &gInv[i])
		z[i].Mul(&z[i], &acc)
	}

	return z

}

// evalConstraints computes the evaluation of lL+qrR+qqmL.R+qoO+k on
// the odd cosets of (Z/8mZ)/(Z/mZ), where m=nbConstraints+nbAssertions.
func evalConstraints(pk *ProvingKey, evalL, evalR, evalO, qk []fr.Element) []fr.Element {

	// evaluates ql, qr, qm, qo, k on the odd cosets of (Z/8mZ)/(Z/mZ)
	evalQl := make([]fr.Element, 4*pk.DomainNum.Cardinality)
	evalQr := make([]fr.Element, 4*pk.DomainNum.Cardinality)
	evalQm := make([]fr.Element, 4*pk.DomainNum.Cardinality)
	evalQo := make([]fr.Element, 4*pk.DomainNum.Cardinality)

	var wg sync.WaitGroup
	wg.Add(2)

	evaluateCosets(pk.Qr, evalQr, &pk.DomainNum)
	go func() {
		for i := 0; i < len(evalQr); i++ {
			// evalQr will contain qr.r
			evalQr[i].Mul(&evalQr[i], &evalR[i])
		}
		wg.Done()
	}()

	evaluateCosets(pk.Qo, evalQo, &pk.DomainNum)
	go func() {
		for i := 0; i < len(evalQo); i++ {
			// evalQo will contain qr.o
			evalQo[i].Mul(&evalQo[i], &evalO[i])
		}
		wg.Done()
	}()

	evaluateCosets(pk.Ql, evalQl, &pk.DomainNum)
	evaluateCosets(pk.Qm, evalQm, &pk.DomainNum)

	var buf fr.Element
	for i := 0; i < len(evalQl); i++ {
		// evalQl will contain (ql + qm.r) * l = ql.l + qm.l.r
		buf.Mul(&evalQm[i], &evalR[i])
		evalQl[i].Add(&evalQl[i], &buf)
		evalQl[i].Mul(&evalQl[i], &evalL[i])
	}

	evalQk := evalQm // we don't need evalQm
	evaluateCosets(qk, evalQk, &pk.DomainNum)

	wg.Wait()

	// computes the evaluation of qrR+qlL+qmL.R+qoO+k on the odd cosets
	// of (Z/8mZ)/(Z/mZ)
	for i := 0; i < len(evalQk); i++ {
		// ql.l + qr.r + qm.l.r + qo.o + k
		evalQk[i].Add(&evalQk[i], &evalQl[i]).
			Add(&evalQk[i], &evalQr[i]).
			Add(&evalQk[i], &evalQo[i])
	}

	return evalQk
}

// evalIDCosets id, uid, u**2id on the odd cosets of (Z/8mZ)/(Z/mZ)
func evalIDCosets(pk *ProvingKey) (id, uid, uuid polynomial.Polynomial) {

	// evaluation of id, uid, u**id on the cosets
	id = make([]fr.Element, 4*pk.DomainNum.Cardinality)
	uid = make([]fr.Element, 4*pk.DomainNum.Cardinality)  // shifter[0]*ID evaluated on odd cosets of (Z/8mZ)/(Z/mZ)
	uuid = make([]fr.Element, 4*pk.DomainNum.Cardinality) // shifter[1]*ID evaluated on odd cosets of (Z/8mZ)/(Z/mZ)

	var uu fr.Element
	uu.Square(&pk.DomainNum.FinerGenerator)
	var u [4]fr.Element
	u[0].Set(&pk.DomainNum.FinerGenerator) // u
	u[1].Mul(&u[0], &uu)                   // u**3
	u[2].Mul(&u[1], &uu)                   // u**5
	u[3].Mul(&u[2], &uu)                   // u**7

	// first elements, id == one * u, ...
	copy(id[:], u[:])

	zn := fr.One()
	for i := 1; i < int(pk.DomainNum.Cardinality); i++ {
		zn.Mul(&zn, &pk.DomainNum.Generator)

		id[4*i].Mul(&zn, &u[0])   // coset u.<1,z,..,z**n-1>
		id[4*i+1].Mul(&zn, &u[1]) // coset u**3.<1,z,..,z**n-1>
		id[4*i+2].Mul(&zn, &u[2]) // coset u**5.<1,z,..,z**n-1>
		id[4*i+3].Mul(&zn, &u[3]) // coset u**7.<1,z,..,z**n-1>
	}
	utils.Parallelize(len(id), func(start, end int) {
		for i := start; i < end; i++ {
			uid[i].Mul(&id[i], &pk.Vk.Shifter[0])  // shifter[0]*ID
			uuid[i].Mul(&id[i], &pk.Vk.Shifter[1]) // shifter[1]*ID
		}
	})

	return
}

// evalConstraintOrdering computes the evaluation of Z(uX)g1g2g3-Z(X)f1f2f3 on the odd
// cosets of (Z/8mZ)/(Z/mZ), where m=nbConstraints+nbAssertions.
//
// z: permutation accumulator polynomial in canonical form
// l, r, o: solution, in canonical form
func evalConstraintOrdering(pk *ProvingKey, evalZ, evalZu, evalL, evalR, evalO polynomial.Polynomial, gamma fr.Element) polynomial.Polynomial {

	// evaluation of z, zu, s1, s2, s3, on the odd cosets of (Z/8mZ)/(Z/mZ)
	evalS1 := make([]fr.Element, 4*pk.DomainNum.Cardinality)
	evalS2 := make([]fr.Element, 4*pk.DomainNum.Cardinality)
	evalS3 := make([]fr.Element, 4*pk.DomainNum.Cardinality)
	evaluateCosets(pk.CS1, evalS1, &pk.DomainNum)
	evaluateCosets(pk.CS2, evalS2, &pk.DomainNum)
	evaluateCosets(pk.CS3, evalS3, &pk.DomainNum)

	// evalutation of ID, u*ID, u**2*ID on the odd cosets of (Z/8mZ)/(Z/mZ)
	evalID, evaluID, evaluuID := evalIDCosets(pk)

	// computes Z(uX)g1g2g3l-Z(X)f1f2f3l on the odd cosets of (Z/8mZ)/(Z/mZ)
	res := make(polynomial.Polynomial, 4*pk.DomainNum.Cardinality)

	var f [3]fr.Element
	var g [3]fr.Element
	for i := 0; i < 4*int(pk.DomainNum.Cardinality); i++ {

		f[0].Add(&evalL[i], &evalID[i]).Add(&f[0], &gamma)   //l_i+z**i+gamma
		f[1].Add(&evalR[i], &evaluID[i]).Add(&f[1], &gamma)  //r_i+u*z**i+gamma
		f[2].Add(&evalO[i], &evaluuID[i]).Add(&f[2], &gamma) //o_i+u**2*z**i+gamma

		g[0].Add(&evalL[i], &evalS1[i]).Add(&g[0], &gamma) //l_i+s1+gamma
		g[1].Add(&evalR[i], &evalS2[i]).Add(&g[1], &gamma) //r_i+s2+gamma
		g[2].Add(&evalO[i], &evalS3[i]).Add(&g[2], &gamma) //o_i+s3+gamma

		f[0].Mul(&f[0], &f[1]).
			Mul(&f[0], &f[2]).
			Mul(&f[0], &evalZ[i]) // z_i*(l_i+z**i+gamma)*(r_i+u*z**i+gamma)*(o_i+u**2*z**i+gamma)

		g[0].Mul(&g[0], &g[1]).
			Mul(&g[0], &g[2]).
			Mul(&g[0], &evalZu[i]) // u*z_i*(l_i+s1+gamma)*(r_i+s2+gamma)*(o_i+s3+gamma)

		res[i].Sub(&g[0], &f[0])
	}

	return res
}

// evalStartsAtOne computes the evaluation of L1*(z-1) on the odd cosets
// of (Z/8mZ)/(Z/mZ).
//
// evalZ is the evaluation of z (=permutation constraint polynomial) on odd cosets of (Z/8mZ)/(Z/mZ)
func evalStartsAtOne(pk *ProvingKey, evalZ polynomial.Polynomial) polynomial.Polynomial {

	// computes L1 (canonical form)
	lOneLagrange := make([]fr.Element, pk.DomainNum.Cardinality)
	lOneLagrange[0].SetOne()
	pk.DomainNum.FFTInverse(lOneLagrange, fft.DIT, 0)
	// TODO @thomas check that DIT witout bitReverse works as intened (DIF + bitReverse)
	// TODO @thomas check that L1 and res can't be pre-computed or computed much faster

	// evaluates L1 on the odd cosets of (Z/8mZ)/(Z/mZ)
	res := make([]fr.Element, 4*pk.DomainNum.Cardinality)
	evaluateCosets(lOneLagrange, res, &pk.DomainNum)

	// // evaluates L1*(z-1) on the odd cosets of (Z/8mZ)/(Z/mZ)
	var buf, one fr.Element
	one.SetOne()
	for i := 0; i < 4*int(pk.DomainNum.Cardinality); i++ {
		buf.Sub(&evalZ[i], &one)
		res[i].Mul(&buf, &res[i])
	}

	return res
}

// evaluateCosets evaluates poly (canonical form) of degree m=domainNum.Cardinality on
// the 4 odd cosets of (Z/8mZ)/(Z/mZ), so it dodges Z/mZ (+Z/2kmZ), which contains the
// vanishing set of Z.
//
// Puts the result in res (of size 4*domain.Cardinality).
//
// Both sizes of poly and res are powers of 2, len(res) = 4*len(poly).
func evaluateCosets(poly, res []fr.Element, domain *fft.Domain) {

	// build a copy of poly padded with 0 so it has the length of the closest power of 2 of poly
	e0 := make([]fr.Element, domain.Cardinality)
	e1 := make([]fr.Element, domain.Cardinality)
	e2 := make([]fr.Element, domain.Cardinality)
	e3 := make([]fr.Element, domain.Cardinality)

	// evaluations[i] must contain poly in the canonical basis
	copy(e0, poly)
	fft.BitReverse(e0)
	copy(e1, e0)
	copy(e2, e0)
	copy(e3, e0)

	var wg sync.WaitGroup
	wg.Add(3)

	domain.FFT(e0, fft.DIT, 1)
	domain.FFT(e1, fft.DIT, 3)
	domain.FFT(e2, fft.DIT, 5)
	domain.FFT(e3, fft.DIT, 7)

	for i := uint64(0); i < domain.Cardinality; i++ {
		res[4*i] = e0[i]
		res[4*i+1] = e1[i]
		res[4*i+2] = e2[i]
		res[4*i+3] = e3[i]
	}
}

// shiftZ turns z to z(uX) (both in Lagrange basis)
func shiftZ(z polynomial.Polynomial) polynomial.Polynomial {

	res := make(polynomial.Polynomial, len(z))

	buf := z[0]
	for i := 0; i < len(res)-1; i++ {
		res[i] = z[i+1]
	}
	res[len(res)-1] = buf

	return res
}

// computeH computes h in canonical form, split as h1+X^mh2+X^2mh3 such that
//
// qlL+qrR+qmL.R+qoO+k + alpha.(zu*g1*g2*g3*l-z*f1*f2*f3*l) + alpha**2*L1*(z-1)= h.Z
// \------------------/         \------------------------/             \-----/
//    constraintsInd			    constraintOrdering					startsAtOne
//
// constraintInd, constraintOrdering are evaluated on the odd cosets of (Z/8mZ)/(Z/mZ)
func computeH(pk *ProvingKey, constraintsInd, constraintOrdering, startsAtOne polynomial.Polynomial, alpha fr.Element) (polynomial.Polynomial, polynomial.Polynomial, polynomial.Polynomial) {

	h := make(polynomial.Polynomial, pk.DomainH.Cardinality)

	// evaluate Z = X**m-1 on the odd cosets of (Z/8mZ)/(Z/mZ)
	var bExpo big.Int
	bExpo.SetUint64(pk.DomainNum.Cardinality)
	var u [4]fr.Element
	var uu fr.Element
	var one fr.Element
	one.SetOne()
	uu.Square(&pk.DomainNum.FinerGenerator)
	u[0].Set(&pk.DomainNum.FinerGenerator)
	u[1].Mul(&u[0], &uu)
	u[2].Mul(&u[1], &uu)
	u[3].Mul(&u[2], &uu)
	u[0].Exp(u[0], &bExpo).Sub(&u[0], &one) // .Inverse(&u[0]) // (X**m-1)**-1 at u
	u[1].Exp(u[1], &bExpo).Sub(&u[1], &one) // .Inverse(&u[1]) // (X**m-1)**-1 at u**3
	u[2].Exp(u[2], &bExpo).Sub(&u[2], &one) // .Inverse(&u[2]) // (X**m-1)**-1 at u**5
	u[3].Exp(u[3], &bExpo).Sub(&u[3], &one) // .Inverse(&u[3]) // (X**m-1)**-1 at u**7
	uinv := fr.BatchInvert(u[:])

	// evaluate qlL+qrR+qmL.R+qoO+k + alpha.(zu*g1*g2*g3*l-z*f1*f2*f3*l) + alpha**2*L1(X)(Z(X)-1)
	// on the odd cosets of (Z/8mZ)/(Z/mZ)
	// and
	// evaluate qlL+qrR+qmL.R+qoO+k + alpha.(zu*g1*g2*g3*l-z*f1*f2*f3*l)/Z
	// on the odd cosets of (Z/8mZ)/(Z/mZ)
	utils.Parallelize(len(h), func(start, end int) {
		for i := start; i < end; i++ {
			h[i].Mul(&startsAtOne[i], &alpha).
				Add(&h[i], &constraintOrdering[i]).
				Mul(&h[i], &alpha).
				Add(&h[i], &constraintsInd[i]).
				Mul(&h[i], &uinv[i%4])
		}
	})

	// put h in canonical form
	pk.DomainH.FFTInverse(h, fft.DIF, 1)
	fft.BitReverse(h)

	h1 := h[:pk.DomainNum.Cardinality]
	h2 := h[pk.DomainNum.Cardinality : 2*pk.DomainNum.Cardinality]
	h3 := h[2*pk.DomainNum.Cardinality : 3*pk.DomainNum.Cardinality]

	return h1, h2, h3

}

// computeLinearizedPolynomial computes the linearized polynomial in canonical basis.
// The purpose is to commit and open all in one ql, qr, qm, qo, qk.
// * a, b, c are the evaluation of l, r, o at zeta
// * z is the permutation polynomial, zu is Z(uX), the shifted version of Z
// * pk is the proving key: the linearized polynomial is a linear combination of ql, qr, qm, qo, qk.
func computeLinearizedPolynomial(l, r, o, alpha, gamma, zeta, zu fr.Element, z polynomial.Polynomial, pk *ProvingKey) polynomial.Polynomial {

	// first part: individual constraints
	var rl fr.Element
	rl.Mul(&r, &l)
	_linearizedPolynomial := pk.Qm.Clone()
	_linearizedPolynomial.ScaleInPlace(&rl) // linPol = lr*Qm

	tmp := pk.Ql.Clone()
	tmp.ScaleInPlace(&l)
	_linearizedPolynomial.Add(_linearizedPolynomial, tmp) // linPol = lr*Qm + l*Ql

	tmp = pk.Qr.Clone()
	tmp.ScaleInPlace(&r)
	_linearizedPolynomial.Add(_linearizedPolynomial, tmp) // linPol = lr*Qm + l*Ql + r*Qr

	tmp = pk.Qo.Clone()
	tmp.ScaleInPlace(&o)
	_linearizedPolynomial.Add(_linearizedPolynomial, tmp) // linPol = lr*Qm + l*Ql + r*Qr + o*Qo

	_linearizedPolynomial.Add(_linearizedPolynomial, pk.CQk) // linPol = lr*Qm + l*Ql + r*Qr + o*Qo + Qk

	// second part: Z(uzeta)(a+s1+gamma)*(b+s2+gamma)*s3(X)-Z(X)(a+zeta+gamma)*(b+uzeta+gamma)*(c+u**2*zeta+gamma)
	var s1, s2, t fr.Element
	s1.SetInterface(pk.CS1.Eval(&zeta)).Add(&s1, &l).Add(&s1, &gamma) // (a+s1+gamma)
	t.SetInterface(pk.CS2.Eval(&zeta)).Add(&t, &r).Add(&t, &gamma)    // (b+s2+gamma)
	s1.Mul(&s1, &t).                                                  // (a+s1+gamma)*(b+s2+gamma)
										Mul(&s1, &zu) // (a+s1+gamma)*(b+s2+gamma)*Z(uzeta)

	s2.Add(&l, &zeta).Add(&s2, &gamma)                          // (a+z+gamma)
	t.Mul(&pk.Vk.Shifter[0], &zeta).Add(&t, &r).Add(&t, &gamma) // (b+uz+gamma)
	s2.Mul(&s2, &t)                                             // (a+z+gamma)*(b+uz+gamma)
	t.Mul(&pk.Vk.Shifter[1], &zeta).Add(&t, &o).Add(&t, &gamma) // (o+u**2z+gamma)
	s2.Mul(&s2, &t)                                             // (a+z+gamma)*(b+uz+gamma)*(c+u**2*z+gamma)
	s2.Neg(&s2)                                                 // -(a+z+gamma)*(b+uz+gamma)*(c+u**2*z+gamma)

	p1 := pk.CS3.Clone()
	p1.ScaleInPlace(&s1) // (a+s1+gamma)*(b+s2+gamma)*Z(uzeta)*s3(X)
	p2 := z.Clone()
	p2.ScaleInPlace(&s2) // -Z(X)(a+zeta+gamma)*(b+uzeta+gamma)*(c+u**2*zeta+gamma)
	p1.Add(p1, p2)
	p1.ScaleInPlace(&alpha) // alpha*( Z(uzeta)*(a+s1+gamma)*(b+s2+gamma)s3(X)-Z(X)(a+zeta+gamma)*(b+uzeta+gamma)*(c+u**2*zeta+gamma) )

	_linearizedPolynomial.Add(_linearizedPolynomial, p1)

	// third part L1(zeta)*alpha**2**Z
	var lagrange, one, den, frNbElmt fr.Element
	one.SetOne()
	nbElmt := int64(pk.DomainNum.Cardinality)
	lagrange.Set(&zeta).
		Exp(lagrange, big.NewInt(nbElmt)).
		Sub(&lagrange, &one)
	frNbElmt.SetUint64(uint64(nbElmt))
	den.Sub(&zeta, &one).
		Mul(&den, &frNbElmt).
		Inverse(&den)
	lagrange.Mul(&lagrange, &den). // L_0 = 1/m*(zeta**n-1)/(zeta-1)
					Mul(&lagrange, &alpha).
					Mul(&lagrange, &alpha) // alpha**2*L_0
	p1 = z.Clone()
	p1.ScaleInPlace(&lagrange)

	// finish the computation
	_linearizedPolynomial.Add(_linearizedPolynomial, p1)

	return _linearizedPolynomial
}
