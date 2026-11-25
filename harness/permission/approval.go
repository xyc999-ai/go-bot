package permission

import (
	"context"
	"encoding/json"
	"errors"

	"go-bot/harness/agent"
	"go-bot/harness/interaction"
	session "go-bot/harness/session"

	"github.com/google/uuid"
)

const Approve = "Allow once"
const Reject = "Deny"

type Request struct {
	Tool   string
	Reason string
	Detail string
	Target Mode
}

type ApprovalService struct {
	Session   *session.LiveSession
	Questions *interaction.NativeQuestions
}

func (s ApprovalService) Request(ctx context.Context, request Request) error {
	turn, step, ok := agent.ToolPositionFromContext(ctx)
	callID, identified := agent.ToolCallIDFromContext(ctx)
	if !ok || !identified {
		return errors.New("approval requires a live tool call")
	}
	id := uuid.NewString()
	appendAudit := func(kind string, fields map[string]string) error {
		data, _ := json.Marshal(fields)
		if _, err := s.Session.Append(session.EventInput{Type: kind, Data: data}); err != nil {
			return err
		}
		return s.Session.Flush(context.WithoutCancel(ctx))
	}
	if err := appendAudit("approval/asked", map[string]string{"id": id, "toolName": request.Tool, "callId": callID, "reason": request.Reason}); err != nil {
		return err
	}
	outcome := "rejected"
	if ReadSession(s.Session).Approval == "ask" {
		answer, err := s.Questions.AskApproval(ctx, turn, step, id,
			interaction.Approval{Tool: request.Tool, Reason: request.Reason, Detail: request.Detail, Target: string(request.Target), ToolCallID: callID},
			[]interaction.Question{{ID: "approval", Question: "Allow this operation?", Options: []interaction.Option{{Label: Approve}, {Label: Reject}}}})
		switch {
		case errors.Is(err, interaction.ErrUnavailable):
			outcome = "unavailable"
		case err != nil:
			outcome = "cancelled"
		case len(answer.Answers) == 1 && answer.Answers[0].Custom == nil &&
			len(answer.Answers[0].Selected) == 1 && answer.Answers[0].Selected[0] == Approve:
			outcome = "allowed-once"
		}
	}
	if err := appendAudit("approval/decided", map[string]string{"id": id, "outcome": outcome}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch outcome {
	case "allowed-once":
		return nil
	case "rejected":
		return Failure{Code: "APPROVAL_REJECTED", Message: "Approval rejected. The requested action did not run."}
	case "cancelled":
		return Failure{Code: "APPROVAL_CANCELLED", Message: "Approval cancelled. The requested action did not run."}
	default:
		return Failure{Code: "APPROVAL_UNAVAILABLE", Message: "No interactive approval channel is available. The requested action did not run."}
	}
}
