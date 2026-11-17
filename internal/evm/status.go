package evm

type Status uint8

const (
	StatusContinue Status = iota
	StatusStop
	StatusReturn
	StatusRevert
	StatusSelfDestruct
	StatusCall
	StatusOutOfGas
	StatusStackUnderflow
	StatusStackOverflow
	StatusInvalidJump
	StatusInvalidOpcode
	StatusStaticViolation
	StatusReturnDataOutOfBounds
	StatusCreateCollision
	StatusCodeSizeExceeded
	StatusInvalidCodePrefix
	StatusInitCodeSizeExceeded
	StatusPrecompileFailure
	StatusNonceOverflow
	StatusDepthExceeded
	StatusInsufficientBalance
	statusCount
)

var statusNames = [statusCount]string{
	StatusContinue:              "continue",
	StatusStop:                  "stop",
	StatusReturn:                "return",
	StatusRevert:                "revert",
	StatusSelfDestruct:          "selfdestruct",
	StatusCall:                  "call",
	StatusOutOfGas:              "out of gas",
	StatusStackUnderflow:        "stack underflow",
	StatusStackOverflow:         "stack overflow",
	StatusInvalidJump:           "invalid jump",
	StatusInvalidOpcode:         "invalid opcode",
	StatusStaticViolation:       "write protection",
	StatusReturnDataOutOfBounds: "return data out of bounds",
	StatusCreateCollision:       "create collision",
	StatusCodeSizeExceeded:      "code size exceeded",
	StatusInvalidCodePrefix:     "invalid code prefix",
	StatusInitCodeSizeExceeded:  "initcode size exceeded",
	StatusPrecompileFailure:     "precompile failure",
	StatusNonceOverflow:         "nonce overflow",
	StatusDepthExceeded:         "call depth exceeded",
	StatusInsufficientBalance:   "insufficient balance",
}

func (s Status) String() string {
	if s < statusCount {
		return statusNames[s]
	}
	return "unknown status"
}

func (s Status) Success() bool {
	return s == StatusStop || s == StatusReturn || s == StatusSelfDestruct
}

func (s Status) Reverted() bool {
	return s == StatusRevert
}

func (s Status) Halted() bool {
	return s >= StatusOutOfGas
}
