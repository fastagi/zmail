package operatorcommands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/fastagi/zmail/api/internal/bootstrap"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/wiring"
)

// maxProvisionPasswordLine bounds what is read from standard input.
const maxProvisionPasswordLine = 1024

// UserProvisionCommand creates an account, or resets an existing account's password, from the
// server console. The password is read from standard input so it never appears in arguments,
// shell history, or process listings, and the command never prints it.
type UserProvisionCommand struct {
	stdin io.Reader
	out   io.Writer
}

func NewUserProvisionCommand() *UserProvisionCommand {
	return &UserProvisionCommand{stdin: os.Stdin, out: os.Stdout}
}

func (c *UserProvisionCommand) Name() string { return "user:provision" }
func (c *UserProvisionCommand) Description() string {
	return "Create an account, or reset its password, with the password read from standard input"
}
func (c *UserProvisionCommand) Usage() string {
	return "user:provision <email> [--username=<name>] < password-file"
}

type userProvisionArguments struct {
	email    string
	username string
}

func parseUserProvisionArguments(args []string) (userProvisionArguments, error) {
	var parsed userProvisionArguments
	positional := make([]string, 0, 1)
	for _, arg := range args {
		if value, ok := strings.CutPrefix(arg, "--username="); ok {
			parsed.username = strings.TrimSpace(value)
			continue
		}
		if strings.HasPrefix(arg, "--") {
			return userProvisionArguments{}, fmt.Errorf("user:provision: unknown flag %s", arg)
		}
		positional = append(positional, arg)
	}
	if len(positional) != 1 {
		return userProvisionArguments{}, errors.New("user:provision takes exactly one email; the password comes from standard input")
	}
	parsed.email = strings.TrimSpace(positional[0])
	return parsed, nil
}

// readProvisionPassword reads the first line of input without surrounding line endings.
func readProvisionPassword(input io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(input, maxProvisionPasswordLine+1)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password from standard input: %w", err)
	}
	if len(line) > maxProvisionPasswordLine {
		return "", errors.New("the password line on standard input is too long")
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		return "", errors.New("pipe the password on standard input, e.g. user:provision <email> < password-file")
	}
	return password, nil
}

func (c *UserProvisionCommand) Run(args []string) error {
	parsed, err := parseUserProvisionArguments(args)
	if err != nil {
		return err
	}
	password, err := readProvisionPassword(c.stdin)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if loggerErr := bootstrap.InitLogger(cfg); loggerErr != nil {
		return loggerErr
	}
	application, err := wiring.InitApplicationWithConfig(cfg)
	if err != nil {
		return fmt.Errorf("initialize user:provision: %w", err)
	}
	if application.UserProvisioning == nil {
		return errors.New("user:provision is unavailable: the user starter is not assembled")
	}
	ctx, stop := operatorCommandContext()
	defer stop()
	return runUserProvision(ctx, application.UserProvisioning, application.AuditRecorder, parsed, password, c.out)
}

func runUserProvision(
	ctx context.Context,
	provisioner domain.UserProvisioner,
	recorder domain.AuditLogRecorder,
	args userProvisionArguments,
	password string,
	out io.Writer,
) error {
	user, created, err := provisioner.ProvisionUser(ctx, domain.UserProvisionRequest{
		Email:    args.email,
		Username: args.username,
		Password: password,
	})
	if err != nil {
		return fmt.Errorf("user:provision: %w", err)
	}
	operation := "reset"
	if created {
		operation = "create"
	}
	if recorder != nil {
		if auditErr := recorder.Record(ctx, &domain.AuditLog{
			ActorType:  domain.AuditActorSystem,
			Action:     operation,
			Resource:   "users",
			TargetType: "user",
			TargetID:   strconv.FormatUint(uint64(user.ID), 10),
			Result:     domain.AuditResultSuccess,
			Method:     "CLI",
			Path:       "user:provision",
			RouteName:  "console.user.provision",
			StatusCode: 200,
			Metadata:   map[string]any{"operation": operation},
		}); auditErr != nil {
			_, _ = fmt.Fprintln(out, "Warning: the account changed, but audit persistence failed")
		}
	}
	if created {
		_, _ = fmt.Fprintf(out, "Created account %s (id %d, username %s)\n", user.Email, user.ID, user.Username)
		return nil
	}
	_, _ = fmt.Fprintf(out, "Reset the password of %s (id %d); its sessions were revoked\n", user.Email, user.ID)
	return nil
}
