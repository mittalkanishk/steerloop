package main_test

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// CLISuite is the external test suite for the steerloop command.
type CLISuite struct {
	suite.Suite
}

func TestCLISuite(t *testing.T) {
	suite.Run(t, new(CLISuite))
}

func (s *CLISuite) SetupTest() {}
