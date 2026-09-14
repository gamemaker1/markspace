package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// user serves "markspace user".
func (c *cli) user(args []string) error {
	return c.group("user", args, map[string]func([]string) error{
		"list":    c.userList,
		"add":     c.userAdd,
		"passwd":  c.userPasswd,
		"admin":   c.userAdmin,
		"disable": c.userActive("user disable", false),
		"enable":  c.userActive("user enable", true),
	})
}

// userList serves "markspace user list".
func (c *cli) userList(args []string) error {
	fs := c.flags("user list")
	rf := addRemoteFlags(fs, true)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := c.parse(fs, "user list", args); err != nil {
		return err
	}
	client, err := rf.client(true)
	if err != nil {
		return err
	}
	ctx, cancel := signalContext(callTimeout)
	defer cancel()
	users, err := client.Users(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		if users == nil {
			users = []*core.User{}
		}
		return writeJSON(c.stdout, users)
	}
	tw := tabwriter.NewWriter(c.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "EMAIL\tNAME\tROLE\tSTATE\tLAST LOGIN")
	for _, u := range users {
		role, state, last := "user", "active", "never"
		if u.IsAdmin {
			role = "admin"
		}
		if !u.IsActive {
			state = "disabled"
		}
		if !u.LastLoginAt.IsZero() {
			last = u.LastLoginAt.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", u.Email, u.Name, role, state, last)
	}
	return tw.Flush()
}

// requireEmail returns the -email value or a usage error.
func requireEmail(cmd, email string) (string, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", usageError{cmd: cmd, msg: "-email is required"}
	}
	return email, nil
}

// userAdd serves "markspace user add".
func (c *cli) userAdd(args []string) error {
	fs := c.flags("user add")
	rf := addRemoteFlags(fs, true)
	email := fs.String("email", "", "email address of the account, required")
	name := fs.String("name", "", "display name, default the part of the email before @")
	admin := fs.Bool("admin", false, "grant admin rights")
	fromStdin := fs.Bool("password-stdin", false, "read the password from the first line of standard input")
	if err := c.parse(fs, "user add", args); err != nil {
		return err
	}
	addr, err := requireEmail("user add", *email)
	if err != nil {
		return err
	}
	password, generated, err := c.password(*fromStdin)
	if err != nil {
		return err
	}
	client, err := rf.client(true)
	if err != nil {
		return err
	}
	in := core.UserInput{Email: &addr, Password: &password, IsAdmin: admin}
	if n := strings.TrimSpace(*name); n != "" {
		in.Name = &n
	}
	ctx, cancel := signalContext(callTimeout)
	defer cancel()
	u, err := client.CreateUser(ctx, in)
	if err != nil {
		return err
	}
	role := ""
	if u.IsAdmin {
		role = " with admin rights"
	}
	fmt.Fprintf(c.stdout, "created %s%s\n", u.Email, role)
	if generated {
		fmt.Fprintf(c.stdout, "password: %s\n", password)
	}
	return nil
}

// userPasswd serves "markspace user passwd".
func (c *cli) userPasswd(args []string) error {
	fs := c.flags("user passwd")
	rf := addRemoteFlags(fs, true)
	email := fs.String("email", "", "email address of the account, required")
	fromStdin := fs.Bool("password-stdin", false, "read the password from the first line of standard input")
	if err := c.parse(fs, "user passwd", args); err != nil {
		return err
	}
	addr, err := requireEmail("user passwd", *email)
	if err != nil {
		return err
	}
	password, generated, err := c.password(*fromStdin)
	if err != nil {
		return err
	}
	client, err := rf.client(true)
	if err != nil {
		return err
	}
	ctx, cancel := signalContext(callTimeout)
	defer cancel()
	u, err := client.UserByEmail(ctx, addr)
	if err != nil {
		return err
	}
	if _, err := client.UpdateUser(ctx, u.ID, core.UserInput{Password: &password}); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "reset the password of %s and signed out its sessions\n", u.Email)
	if generated {
		fmt.Fprintf(c.stdout, "password: %s\n", password)
	}
	return nil
}

// userAdmin serves "markspace user admin".
func (c *cli) userAdmin(args []string) error {
	fs := c.flags("user admin")
	rf := addRemoteFlags(fs, true)
	email := fs.String("email", "", "email address of the account, required")
	revoke := fs.Bool("revoke", false, "revoke admin rights instead of granting them")
	if err := c.parse(fs, "user admin", args); err != nil {
		return err
	}
	addr, err := requireEmail("user admin", *email)
	if err != nil {
		return err
	}
	client, err := rf.client(true)
	if err != nil {
		return err
	}
	ctx, cancel := signalContext(callTimeout)
	defer cancel()
	u, err := client.UserByEmail(ctx, addr)
	if err != nil {
		return err
	}
	grant := !*revoke
	if _, err := client.UpdateUser(ctx, u.ID, core.UserInput{IsAdmin: &grant}); err != nil {
		return err
	}
	if grant {
		fmt.Fprintf(c.stdout, "%s has admin rights\n", u.Email)
	} else {
		fmt.Fprintf(c.stdout, "%s has no admin rights\n", u.Email)
	}
	return nil
}

// userActive returns the handler of "markspace user enable" or "markspace
// user disable".
func (c *cli) userActive(cmd string, active bool) func([]string) error {
	return func(args []string) error {
		fs := c.flags(cmd)
		rf := addRemoteFlags(fs, true)
		email := fs.String("email", "", "email address of the account, required")
		if err := c.parse(fs, cmd, args); err != nil {
			return err
		}
		addr, err := requireEmail(cmd, *email)
		if err != nil {
			return err
		}
		client, err := rf.client(true)
		if err != nil {
			return err
		}
		ctx, cancel := signalContext(callTimeout)
		defer cancel()
		u, err := client.UserByEmail(ctx, addr)
		if err != nil {
			return err
		}
		if _, err := client.UpdateUser(ctx, u.ID, core.UserInput{IsActive: &active}); err != nil {
			return err
		}
		if active {
			fmt.Fprintf(c.stdout, "enabled %s\n", u.Email)
		} else {
			fmt.Fprintf(c.stdout, "disabled %s and signed out its sessions\n", u.Email)
		}
		return nil
	}
}

// password reads the password from the first line of standard input, or
// generates one. generated reports which.
func (c *cli) password(fromStdin bool) (password string, generated bool, err error) {
	if !fromStdin {
		p, err := generatePassword()
		return p, true, err
	}
	line, err := bufio.NewReader(io.LimitReader(c.stdin, 64<<10)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, fmt.Errorf("read password: %w", err)
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", false, errors.New("no password on standard input")
	}
	return line, false, nil
}

// generatePassword returns 24 characters of URL-safe base64 from 18 random
// bytes, which is 144 bits.
func generatePassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
