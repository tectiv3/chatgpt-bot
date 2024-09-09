package main

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	// tele "gopkg.in/telebot.v3"
	"strconv"
)

type Step struct {
	Field  string
	Prompt string
	Input  *string
	Next   *Step
}

type State struct {
	ID        *uint
	Name      string
	FirstStep Step
}

// Value implements the driver.Valuer interface, allowing
// for converting the State to a JSON string for database storage.
func (s State) Value() (driver.Value, error) {
	if s.ID == nil && s.Name == "" && s.FirstStep == (Step{}) {
		return nil, nil
	}
	return json.Marshal(s)
}

// Scan implements the sql.Scanner interface, allowing for
// converting a JSON string from the database back into the State slice.
func (s *State) Scan(value interface{}) error {
	if value == nil {
		s = nil
		return nil
	}

	b, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("type assertion to []byte failed")
	}

	return json.Unmarshal(b, &s)
}

func findEmptyStep(step *Step) *Step {
	if step.Input != nil {
		if step.Next == nil {
			return nil
		}

		return findEmptyStep(step.Next)
	}

	return step
}

func as_uint(s string) uint {
	i, _ := strconv.Atoi(s)

	return uint(i)
}
