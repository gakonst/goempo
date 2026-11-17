package kzg

import (
	"crypto/sha256"
	"errors"

	"github.com/gakonst/goempo/internal/crypto/bls12381"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	InputLength          = 192
	OutputLength         = 64
	Gas                  = 50000
	FieldElementsPerBlob = 4096
	VersionedHashVersion = 0x01
	hashLength           = 32
	scalarLength         = 32
	compressedG1Length   = 48
	commitmentOffset     = hashLength + 2*scalarLength
	proofOffset          = commitmentOffset + compressedG1Length
)

const _ = uint(InputLength - proofOffset - compressedG1Length)

const _ = uint(proofOffset + compressedG1Length - InputLength)

const _ = uint(Gas - FieldElementsPerBlob)

var (
	ErrInputLength   = errors.New("kzg: input must be 192 bytes")
	ErrVersionedHash = errors.New("kzg: versioned hash does not match the commitment")
	ErrFieldElement  = errors.New("kzg: z or y is not below the BLS modulus")
	ErrInvalidPoint  = errors.New("kzg: invalid commitment or proof")
	ErrProof         = errors.New("kzg: proof does not verify")
)

var blsModulus = u256.Int{0xffffffff00000001, 0x53bda402fffe5bfe, 0x3339d80809a1d805, 0x73eda753299d7d48}

func VersionedHash(commitment [compressedG1Length]byte) [hashLength]byte {
	h := sha256.Sum256(commitment[:])
	h[0] = VersionedHashVersion
	return h
}

func Output() [OutputLength]byte {
	var out [OutputLength]byte
	u256.FromUint64(FieldElementsPerBlob).PutBytes32(out[:scalarLength])
	blsModulus.PutBytes32(out[scalarLength:])
	return out
}

func PointEvaluation(input []byte) ([OutputLength]byte, error) {
	var none [OutputLength]byte
	if len(input) != InputLength {
		return none, ErrInputLength
	}
	var commitment, proof [compressedG1Length]byte
	copy(commitment[:], input[commitmentOffset:proofOffset])
	copy(proof[:], input[proofOffset:])
	if [hashLength]byte(input[:hashLength]) != VersionedHash(commitment) {
		return none, ErrVersionedHash
	}
	z := u256.FromBytes(input[hashLength : hashLength+scalarLength])
	y := u256.FromBytes(input[hashLength+scalarLength : commitmentOffset])
	if !z.Lt(blsModulus) || !y.Lt(blsModulus) {
		return none, ErrFieldElement
	}
	ok, err := verify(claim{commitment: commitment, proof: proof, z: z, y: y})
	if err != nil {
		return none, err
	}
	if !ok {
		return none, ErrProof
	}
	return Output(), nil
}

type claim struct {
	commitment [compressedG1Length]byte
	proof      [compressedG1Length]byte
	z          u256.Int
	y          u256.Int
}

func verify(c claim) (bool, error) {
	cPoint, err := bls12381.DecompressG1(c.commitment)
	if err != nil {
		return false, ErrInvalidPoint
	}
	proofPoint, err := bls12381.DecompressG1(c.proof)
	if err != nil {
		return false, ErrInvalidPoint
	}
	msm1 := make([]byte, 0, 2*bls12381.G1MSMPairLength)
	msm1 = append(msm1, cPoint[:]...)
	msm1 = appendScalar(msm1, u256.One())
	msm1 = append(msm1, g1Generator[:]...)
	msm1 = appendScalar(msm1, blsModulus.Sub(c.y))
	p1, err := bls12381.G1MSM(msm1)
	if err != nil {
		return false, err
	}
	msm2 := make([]byte, 0, 2*bls12381.G2MSMPairLength)
	msm2 = append(msm2, tauG2[:]...)
	msm2 = appendScalar(msm2, u256.One())
	msm2 = append(msm2, negG2[:]...)
	msm2 = appendScalar(msm2, c.z)
	p2, err := bls12381.G2MSM(msm2)
	if err != nil {
		return false, err
	}
	pairing := make([]byte, 0, 2*bls12381.PairingLength)
	pairing = append(pairing, p1[:]...)
	pairing = append(pairing, negG2[:]...)
	pairing = append(pairing, proofPoint[:]...)
	pairing = append(pairing, p2[:]...)
	result, err := bls12381.PairingCheck(pairing)
	if err != nil {
		return false, err
	}
	return result[bls12381.OutputWord-1] == 1, nil
}

func appendScalar(dst []byte, s u256.Int) []byte {
	b := s.Bytes32()
	return append(dst, b[:]...)
}
