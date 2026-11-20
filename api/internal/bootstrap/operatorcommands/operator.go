package operatorcommands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/fastagi/zmail/api/internal/bootstrap"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/console"
	"github.com/fastagi/zmail/api/internal/wiring"
)

var errOperatorCommandUnavailable = errors.New("operator command service is unavailable")

type operatorCommandRuntime struct {
	grants domain.OperatorGrantStore
	audit  domain.AuditLogRecorder
}

// OperatorGrantCommand makes an existing account a platform operator.
type OperatorGrantCommand struct {
	output *console.Output
}

func NewOperatorGrantCommand() *OperatorGrantCommand {
	return &OperatorGrantCommand{output: console.NewOutput()}
}

func (c *OperatorGrantCommand) Name() string { return "operator:grant" }
func (c *OperatorGrantCommand) Description() string {
	return "Grant platform-operator access to an account"
}
func (c *OperatorGrantCommand) Usage() string {
	return "operator:grant <email> [--scope=full|mail-metrics]"
}

func (c *OperatorGrantCommand) Run(args []string) error {
	email, scope, err := parseOperatorGrantArguments(args)
	if err != nil {
		return err
	}
	runtime, err := loadOperatorCommandRuntime()
	if err != nil {
		return err
	}
	ctx, stop := operatorCommandContext()
	defer stop()
	grant, change, err := runtime.grants.GrantOperator(ctx, email, scope)
	if err != nil {
		return operatorCommandError(err)
	}
	if change == domain.OperatorGrantUnchanged {
		c.output.Info("%s is already a platform operator with scope %s", grant.Email, grant.Scope)
		return nil
	}
	if auditErr := recordOperatorCommandAudit(ctx, runtime.audit, "grant", grant.UserID, grant.Scope); auditErr != nil {
		c.output.Warning("Operator granted, but audit persistence failed")
	}
	c.output.Success("Granted platform-operator access with scope %s to %s", grant.Scope, grant.Email)
	return nil
}

// parseOperatorGrantArguments reads `<email> [--scope=full|mail-metrics]`; the scope defaults to full.
func parseOperatorGrantArguments(args []string) (string, domain.OperatorScope, error) {
	scope := domain.OperatorScopeFull
	positional := make([]string, 0, 1)
	for _, arg := range args {
		value, isScope := strings.CutPrefix(arg, "--scope=")
		if !isScope {
			positional = append(positional, arg)
			continue
		}
		switch strings.TrimSpace(value) {
		case "full":
			scope = domain.OperatorScopeFull
		case "mail-metrics":
			scope = domain.OperatorScopeMailMetrics
		default:
			return "", "", fmt.Errorf("operator:grant --scope must be full or mail-metrics")
		}
	}
	email, err := operatorEmailArgument("operator:grant", positional)
	if err != nil {
		return "", "", err
	}
	return email, scope, nil
}

// OperatorRevokeCommand removes platform-operator access from an account.
type OperatorRevokeCommand struct {
	output *console.Output
}

func NewOperatorRevokeCommand() *OperatorRevokeCommand {
	return &OperatorRevokeCommand{output: console.NewOutput()}
}

func (c *OperatorRevokeCommand) Name() string { return "operator:revoke" }
func (c *OperatorRevokeCommand) Description() string {
	return "Revoke platform-operator access from an account"
}
func (c *OperatorRevokeCommand) Usage() string { return "operator:revoke <email>" }

func (c *OperatorRevokeCommand) Run(args []string) error {
	email, err := operatorEmailArgument("operator:revoke", args)
	if err != nil {
		return err
	}
	runtime, err := loadOperatorCommandRuntime()
	if err != nil {
		return err
	}
	ctx, stop := operatorCommandContext()
	defer stop()
	userID, removed, err := runtime.grants.RevokeOperator(ctx, email)
	if err != nil {
		return operatorCommandError(err)
	}
	if !removed {
		c.output.Info("%s is not a platform operator", email)
		return nil
	}
	if auditErr := recordOperatorCommandAudit(ctx, runtime.audit, "revoke", userID, ""); auditErr != nil {
		c.output.Warning("Operator revoked, but audit persistence failed")
	}
	c.output.Success("Revoked platform-operator access from %s", email)
	return nil
}

// OperatorListCommand lists current platform operators.
type OperatorListCommand struct {
	output *console.Output
}

func NewOperatorListCommand() *OperatorListCommand {
	return &OperatorListCommand{output: console.NewOutput()}
}

func (c *OperatorListCommand) Name() string        { return "operator:list" }
func (c *OperatorListCommand) Description() string { return "List platform operators" }
func (c *OperatorListCommand) Usage() string       { return "operator:list" }

func (c *OperatorListCommand) Run(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("operator:list accepts no arguments")
	}
	runtime, err := loadOperatorCommandRuntime()
	if err != nil {
		return err
	}
	ctx, stop := operatorCommandContext()
	defer stop()
	grants, err := runtime.grants.ListOperators(ctx)
	if err != nil {
		return operatorCommandError(err)
	}
	rows := make([][]string, len(grants))
	for index, grant := range grants {
		rows[index] = []string{
			strconv.FormatUint(uint64(grant.UserID), 10),
			grant.Username,
			grant.Email,
			string(grant.Scope),
			grant.GrantedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	c.output.Table([]string{"USER ID", "USERNAME", "EMAIL", "SCOPE", "GRANTED AT"}, rows)
	return nil
}

func operatorEmailArgument(command string, args []string) (string, error) {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return "", fmt.Errorf("%s requires exactly one email argument", command)
	}
	return strings.TrimSpace(args[0]), nil
}

func operatorCommandError(err error) error {
	if errors.Is(err, domain.ErrUserNotFound) {
		return fmt.Errorf("no account uses that email")
	}
	return err
}

func loadOperatorCommandRuntime() (*operatorCommandRuntime, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if !cfg.Starters.Selected(config.StarterOperator) {
		return nil, fmt.Errorf("operator starter is not selected in OPTIONAL_STARTERS")
	}
	if loggerErr := bootstrap.InitLogger(cfg); loggerErr != nil {
		return nil, loggerErr
	}
	application, err := wiring.InitApplicationWithConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("initialize operator command: %w", err)
	}
	if application.OperatorGrants == nil || application.AuditRecorder == nil {
		return nil, errOperatorCommandUnavailable
	}
	return &operatorCommandRuntime{grants: application.OperatorGrants, audit: application.AuditRecorder}, nil
}

func operatorCommandContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// recordOperatorCommandAudit records a grant change with the system as actor and the account's user
// ID as target. The email is not stored in the audit record; a grant records its scope.
func recordOperatorCommandAudit(
	ctx context.Context,
	recorder domain.AuditLogRecorder,
	operation string,
	userID uint,
	scope domain.OperatorScope,
) error {
	if recorder == nil {
		return errOperatorCommandUnavailable
	}
	targetID := strconv.FormatUint(uint64(userID), 10)
	command := "operator:" + operation
	return recorder.Record(ctx, &domain.AuditLog{
		ActorType:  domain.AuditActorSystem,
		Action:     operation,
		Resource:   "platform_operators",
		TargetType: "user",
		TargetID:   targetID,
		Result:     domain.AuditResultSuccess,
		Method:     "CLI",
		Path:       command,
		RouteName:  "console.operator." + operation,
		StatusCode: 200,
		Metadata:   operatorAuditMetadata(operation, scope),
	})
}

func operatorAuditMetadata(operation string, scope domain.OperatorScope) map[string]any {
	metadata := map[string]any{"operation": operation}
	if scope != "" {
		metadata["scope"] = string(scope)
	}
	return metadata
}
