// Package gitsrc fetches a project's source from a git repository over SSH with a deploy key.
package gitsrc

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kballard/go-shellquote"
	"golang.org/x/crypto/ssh"

	"github.com/kotlovyim-dev/kayly/internal/store"
)

// GenerateKey returns a new ed25519 key: the private half as an OpenSSH PEM, the public half
// as an authorized_keys line to paste into the repository's deploy keys.
func GenerateKey(comment string) (private []byte, public string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, "", err
	}
	return pem.EncodeToMemory(block), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment, nil
}

// PublicKey derives the authorized_keys line from a private key made by GenerateKey.
func PublicKey(private []byte) (string, error) {
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}

// Only SSH is accepted: the deploy key is the credential. The first character rules out
// anything git could take for an option.
var (
	scpURL = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*@[A-Za-z0-9][A-Za-z0-9.-]*:[A-Za-z0-9_./~-]+$`)
	sshURL = regexp.MustCompile(`^ssh://[A-Za-z0-9_][A-Za-z0-9_.-]*@[A-Za-z0-9][A-Za-z0-9.-]*(:[0-9]+)?/[A-Za-z0-9_./~-]+$`)
	refRe  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/+@-]*$`)
)

// ValidURL accepts git@host:owner/repo.git and ssh://git@host[:port]/owner/repo.git.
func ValidURL(url string) error {
	if !scpURL.MatchString(url) && !sshURL.MatchString(url) || strings.Contains(url, "..") {
		return fmt.Errorf("%q is not an SSH git URL", url)
	}
	return nil
}

// ValidRef accepts a branch, tag or commit.
func ValidRef(ref string) error {
	if !refRe.MatchString(ref) || strings.Contains(ref, "..") {
		return fmt.Errorf("%q is not a branch, tag or commit", ref)
	}
	return nil
}

// Fetcher clones repositories.
type Fetcher struct {
	// KnownHosts is where host keys are remembered: the first connection to a host trusts
	// its key, later ones must match.
	KnownHosts string
}

// Fetch puts the files of ref (a branch, tag or commit) of url into dir, without history,
// and returns the commit they are from.
func (f Fetcher) Fetch(ctx context.Context, url, ref string, privateKey []byte, dir string) (store.Commit, error) {
	if err := ValidURL(url); err != nil {
		return store.Commit{}, err
	}
	if err := ValidRef(ref); err != nil {
		return store.Commit{}, err
	}
	keyDir, err := os.MkdirTemp("", "kayly-key-")
	if err != nil {
		return store.Commit{}, err
	}
	defer os.RemoveAll(keyDir)
	keyFile := filepath.Join(keyDir, "id")
	if err := os.WriteFile(keyFile, privateKey, 0o600); err != nil {
		return store.Commit{}, err
	}
	sshCmd := shellquote.Join("ssh", "-i", keyFile, "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile="+f.KnownHosts)
	return fetch(ctx, url, ref, dir, []string{"GIT_SSH_COMMAND=" + sshCmd, "GIT_ALLOW_PROTOCOL=ssh"})
}

func fetch(ctx context.Context, url, ref, dir string, env []string) (store.Commit, error) {
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"PATH="+os.Getenv("PATH"))
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
		}
		return out.String(), nil
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"fetch", "-q", "--depth", "1", "--no-tags", "--", url, ref},
		{"-c", "advice.detachedHead=false", "checkout", "-q", "--detach", "FETCH_HEAD"},
	} {
		if _, err := git(args...); err != nil {
			return store.Commit{}, err
		}
	}
	out, err := git("log", "-1", "--format=%H%n%s")
	if err != nil {
		return store.Commit{}, err
	}
	sha, msg, _ := strings.Cut(strings.TrimSpace(out), "\n")
	// The build context is the work tree; history would only slow the upload to BuildKit.
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		return store.Commit{}, err
	}
	return store.Commit{SHA: sha, Message: msg}, nil
}
