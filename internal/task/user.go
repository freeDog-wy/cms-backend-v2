package task

import (
	"context"
	"encoding/json"
	"fmt"
)

type UserTask struct {
	
}

func ATaskOccured(ctx context.Context, payload json.RawMessage) error {
	fmt.Println("task occured!")
	return nil
}
