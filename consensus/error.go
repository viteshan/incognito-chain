package consensus

import (
	"fmt"

	"github.com/pkg/errors"
)

const (
	UnExpectedError = iota
	ConsensusTypeNotExistError
	ProducerSignatureError
	CommitteeSignatureError
	CombineSignatureError
	SignDataError
	LoadKeyError
)

var ErrCodeMessage = map[int]struct {
	Code    int
	message string
}{
	UnExpectedError:            {-1000, "Unexpected error"},
	ConsensusTypeNotExistError: {-1001, "Consensus type isn't exist"},
	ProducerSignatureError:     {-1002, "Producer signature error"},
	CommitteeSignatureError:    {-1003, "Committee signature error"},
	CombineSignatureError:      {-1004, "Combine signature error"},
	SignDataError:              {-1005, "Sign data error"},
	LoadKeyError:               {-1006, "Load key error"},
}

type ConsensusError struct {
	Code    int
	Message string
	err     error
}

func (e ConsensusError) Error() string {
	return fmt.Sprintf("%d: %s \n %+v", e.Code, e.Message, e.err)
}

func NewConsensusError(key int, err error) error {
	return &ConsensusError{
		Code:    ErrCodeMessage[key].Code,
		Message: ErrCodeMessage[key].message,
		err:     errors.Wrap(err, ErrCodeMessage[key].message),
	}
}
