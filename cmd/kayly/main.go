// Command kayly is both the Kayly daemon (`kayly serve`) and its CLI.
package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/api"
	"github.com/kotlovyim-dev/kayly/internal/config"
	"github.com/kotlovyim-dev/kayly/internal/deploy"
	"github.com/kotlovyim-dev/kayly/internal/offsite"
)

const usage = `kayly deploys apps to your own server.

On the server:
  kayly serve --base-domain <ip>.sslip.io   run the daemon (installed as a systemd unit)
  kayly token create <name> [-scope read|deploy|admin] [-p project] [-expires-days n]
                                            create an API token for a laptop, CI or an agent
  kayly token ls | token rm <id>
  kayly audit [-n lines]                    who changed what

Anywhere:
  kayly login <url> <token>                 remember a server
  kayly deploy [dir] [-p project]           build and release dir (default .)
  kayly projects                            list projects
  kayly status [-p project]                 the server, and the services of one project or of all: state, restarts, CPU, memory
  kayly releases [-p project]               list releases
  kayly releases log <release> [-p project] the output of that deploy
  kayly rollback <release> [-p project]     make an earlier release live again
  kayly logs <service> [-p project] [-f] [-n lines]
  kayly redeploy [-p project]               release the live image again with current env and add-ons
  kayly env ls | set KEY=value... | rm KEY... [-p project]
  kayly domains ls | add <hostname> <service> | rm <hostname> [-p project]
  kayly addons ls | add <postgres|redis> | rm <kind> [-p project]
  kayly backups ls [-remote] | create | get <name> [-p project]
  kayly backups restore <name|latest> [-remote] [-from project] [-p project]
                                            replaces live data; asks for the project name first
  kayly backups setup <endpoint> <bucket> <access-key> [region]   off-site S3; the secret key comes on stdin
  kayly backups status | sync | drill | kayly   kayly: snapshot Kayly's own database and key
  kayly restore -endpoint E -bucket B -access-key K -recovery-key KEY [-region R]
                                            on a new server, before starting the daemon: brings back
                                            the database, key and newest backups; the secret key comes on stdin
  kayly notify ls | telegram <chat-id> | webhook <url> | off telegram|webhook | test
                                            telegram reads the bot token from stdin

The project defaults to the directory name. On the server, the CLI talks to the daemon's
admin socket (in $KAYLY_RUN, default /run/kayly) and needs no login.
`

// version is set at release time: -ldflags "-X main.version=v0.2.0".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "login":
		err = login(args)
	case "restore":
		err = restoreServer(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		err = runClientCommand(cmd, args)
	}
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.hint != "" {
			fmt.Fprintf(os.Stderr, "error: %s\n  %s\n", ae.msg, ae.hint)
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		os.Exit(1)
	}
}

func runClientCommand(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	project := fs.String("p", "", "project (default: the current directory's name)")
	follow := fs.Bool("f", false, "follow the output")
	lines := fs.Int("n", 100, "number of lines")
	remote := fs.Bool("remote", false, "backups: use the off-site bucket")
	scope := fs.String("scope", "admin", "token create: read, deploy or admin")
	expires := fs.Int("expires-days", 0, "token create: expire after this many days (0: never)")
	from := fs.String("from", "", "backups restore: the project the backup was taken from")
	// Flags may come before or after positional arguments.
	var pos []string
	for {
		fs.Parse(args)
		if fs.NArg() == 0 {
			break
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	proj := func(dir string) (string, error) {
		if *project != "" {
			return *project, nil
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		name := filepath.Base(abs)
		if !config.ValidName(name) {
			return "", fmt.Errorf("%q is not a valid project name; pass -p <name>", name)
		}
		return name, nil
	}
	arg := func(i int) string {
		if i < len(pos) {
			return pos[i]
		}
		return ""
	}

	switch cmd {
	case "deploy":
		dir := arg(0)
		if dir == "" {
			dir = "."
		}
		name, err := proj(dir)
		if err != nil {
			return err
		}
		return c.deploy(ctx, name, dir)
	case "projects":
		var ps []api.ProjectView
		if err := c.do(ctx, "GET", "/api/v1/projects", nil, &ps); err != nil {
			return err
		}
		tw := table()
		fmt.Fprintln(tw, "PROJECT\tLIVE\tSINCE")
		for _, p := range ps {
			live := "-"
			if p.LiveSeq != 0 {
				live = "r" + strconv.Itoa(p.LiveSeq)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, live, p.LiveSince)
		}
		return tw.Flush()
	case "status":
		return c.status(ctx, *project)
	case "releases":
		name, err := proj(".")
		if err != nil {
			return err
		}
		if len(pos) == 2 && pos[0] == "log" {
			return c.releaseLog(ctx, name, pos[1])
		}
		var rs []api.ReleaseView
		if err := c.do(ctx, "GET", "/api/v1/projects/"+name+"/releases", nil, &rs); err != nil {
			return err
		}
		tw := table()
		fmt.Fprintln(tw, "RELEASE\tSTATUS\tCREATED\tCOMMIT\tERROR")
		for _, r := range rs {
			fmt.Fprintf(tw, "r%d\t%s\t%s\t%s\t%s\n", r.Seq, r.Status, r.CreatedAt, shortCommit(r), firstLine(r.Error))
		}
		return tw.Flush()
	case "rollback":
		name, err := proj(".")
		if err != nil {
			return err
		}
		seq, err := strconv.Atoi(strings.TrimPrefix(arg(0), "r"))
		if err != nil {
			return errors.New("usage: kayly rollback <release>, e.g. kayly rollback r12")
		}
		return c.stream(ctx, "/api/v1/projects/"+name+"/rollback", "application/json",
			strings.NewReader(fmt.Sprintf(`{"release":%d}`, seq)))
	case "logs":
		name, err := proj(".")
		if err != nil {
			return err
		}
		if arg(0) == "" {
			return errors.New("usage: kayly logs <service>")
		}
		q := url.Values{"service": {arg(0)}, "tail": {strconv.Itoa(*lines)}}
		if *follow {
			q.Set("follow", "1")
		}
		resp, err := c.request(ctx, "GET", "/api/v1/projects/"+name+"/logs?"+q.Encode(), "", nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, err = io.Copy(os.Stdout, resp.Body)
		return err
	case "redeploy":
		name, err := proj(".")
		if err != nil {
			return err
		}
		return c.stream(ctx, "/api/v1/projects/"+name+"/redeploy", "", nil)
	case "addons":
		return c.addons(ctx, proj, pos)
	case "backups":
		return c.backups(ctx, proj, pos, *remote, *from)
	case "env":
		return c.env(ctx, proj, pos)
	case "domains":
		return c.domains(ctx, proj, pos)
	case "token":
		return c.tokens(ctx, pos, *scope, *project, *expires)
	case "audit":
		return c.audit(ctx, *lines)
	case "notify":
		return c.notify(ctx, pos)
	}
	return fmt.Errorf("unknown command %q; run kayly help", cmd)
}

func (c *client) env(ctx context.Context, proj func(string) (string, error), pos []string) error {
	name, err := proj(".")
	if err != nil {
		return err
	}
	base := "/api/v1/projects/" + name + "/env"
	if len(pos) == 0 || pos[0] == "ls" {
		var v api.EnvView
		if err := c.do(ctx, "GET", base, nil, &v); err != nil {
			return err
		}
		for _, k := range v.Keys {
			fmt.Println(k)
		}
		return nil
	}
	switch pos[0] {
	case "set":
		for _, kv := range pos[1:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("%q: want KEY=value", kv)
			}
			if err := c.do(ctx, "PUT", base+"/"+k, map[string]string{"value": v}, nil); err != nil {
				return err
			}
			fmt.Printf("set %s (applies on the next deploy)\n", k)
		}
		return nil
	case "rm":
		for _, k := range pos[1:] {
			if err := c.do(ctx, "DELETE", base+"/"+k, nil, nil); err != nil {
				return err
			}
		}
		return nil
	}
	return errors.New("usage: kayly env ls | set KEY=value [KEY=value] | rm KEY [KEY]")
}

func (c *client) addons(ctx context.Context, proj func(string) (string, error), pos []string) error {
	name, err := proj(".")
	if err != nil {
		return err
	}
	base := "/api/v1/projects/" + name + "/addons"
	show := func(as []deploy.AddonView) error {
		tw := table()
		fmt.Fprintln(tw, "ADD-ON\tSTATUS\tSERVICES GET\tIMAGE")
		for _, a := range as {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.Kind, a.Status, a.EnvVar, a.Image)
		}
		return tw.Flush()
	}
	switch {
	case len(pos) == 0 || pos[0] == "ls":
		var as []deploy.AddonView
		if err := c.do(ctx, "GET", base, nil, &as); err != nil {
			return err
		}
		return show(as)
	case pos[0] == "add" && len(pos) == 2:
		fmt.Printf("starting %s…\n", pos[1])
		var as []deploy.AddonView
		if err := c.do(ctx, "POST", base, map[string]string{"kind": pos[1]}, &as); err != nil {
			return err
		}
		if err := show(as); err != nil {
			return err
		}
		fmt.Println("\nServices get it on the next deploy; `kayly redeploy` applies it now.")
		return nil
	case pos[0] == "rm" && len(pos) == 2:
		fmt.Printf("This deletes the %s add-on of %s and all its data (backups are kept).\nType the project name to confirm: ", pos[1], name)
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(answer) != name {
			return errors.New("not confirmed; nothing deleted")
		}
		return c.do(ctx, "DELETE", base+"/"+pos[1], nil, nil)
	}
	return errors.New("usage: kayly addons ls | add <postgres|redis> | rm <kind>")
}

func (c *client) backups(ctx context.Context, proj func(string) (string, error), pos []string, remote bool, from string) error {
	if len(pos) > 0 && (pos[0] == "setup" || pos[0] == "status" || pos[0] == "sync" || pos[0] == "drill" || pos[0] == "kayly") {
		return c.offsite(ctx, pos)
	}
	name, err := proj(".")
	if err != nil {
		return err
	}
	base := "/api/v1/projects/" + name + "/backups"
	switch {
	case len(pos) == 0 || pos[0] == "ls":
		bs, err := c.listBackups(ctx, base, remote)
		if err != nil {
			return err
		}
		tw := table()
		fmt.Fprintln(tw, "BACKUP\tSIZE")
		for _, b := range bs {
			fmt.Fprintf(tw, "%s\t%d KB\n", b.Name, b.Size/1024)
		}
		return tw.Flush()
	case pos[0] == "create":
		var bs []deploy.Backup
		if err := c.do(ctx, "POST", base, nil, &bs); err != nil {
			return err
		}
		if len(bs) == 0 {
			fmt.Println("nothing to back up: the project has no database add-on or volume")
		}
		for _, b := range bs {
			fmt.Printf("%s (%d KB)\n", b.Name, b.Size/1024)
		}
		return nil
	case pos[0] == "restore" && len(pos) == 2:
		return c.restore(ctx, name, pos[1], remote, from)
	case pos[0] == "get" && len(pos) == 2:
		resp, err := c.request(ctx, "GET", base+"/"+url.PathEscape(pos[1]), "", nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		f, err := os.OpenFile(name+"-"+filepath.Base(pos[1]), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, resp.Body); err != nil {
			f.Close()
			return err
		}
		fmt.Printf("saved %s\n", f.Name())
		if strings.HasSuffix(f.Name(), ".dump") {
			fmt.Printf("restore with: pg_restore --clean --no-owner -d <database url> %s\n", f.Name())
		}
		return f.Close()
	}
	return errors.New("usage: kayly backups ls | create | get <name> | restore <name|latest> | setup | status | sync | drill")
}

// status prints the server's disk and memory, then the services of one project, or of all.
func (c *client) status(ctx context.Context, project string) error {
	var sv api.ServerView
	if err := c.do(ctx, "GET", "/api/v1/server", nil, &sv); err != nil {
		return err
	}
	fmt.Printf("kayly %s, docker %s\ndisk   %s of %s used\nmemory %s of %s available\n",
		sv.Version, sv.DockerVersion, deploy.HumanBytes(sv.DiskUsed), deploy.HumanBytes(sv.DiskTotal),
		deploy.HumanBytes(sv.MemoryFree), deploy.HumanBytes(sv.MemoryTotal))
	names := []string{project}
	if project == "" {
		var ps []api.ProjectView
		if err := c.do(ctx, "GET", "/api/v1/projects", nil, &ps); err != nil {
			return err
		}
		names = nil
		for _, p := range ps {
			names = append(names, p.Name)
		}
	}
	for _, name := range names {
		var svcs []deploy.ServiceView
		if err := c.do(ctx, "GET", "/api/v1/projects/"+name+"/services", nil, &svcs); err != nil {
			return err
		}
		fmt.Printf("\n%s\n", name)
		tw := table()
		fmt.Fprintln(tw, "  SERVICE\tSTATE\tRESTARTS\tCPU\tMEMORY")
		for _, s := range svcs {
			cpu, mem := "-", "-"
			if s.Stats != nil {
				cpu = fmt.Sprintf("%.1f%%", s.Stats.CPUPercent)
				mem = deploy.HumanBytes(s.Stats.MemoryBytes)
				if s.MemoryLimit > 0 {
					mem += " / " + deploy.HumanBytes(s.MemoryLimit)
				}
			}
			fmt.Fprintf(tw, "  %s\t%s\t%d\t%s\t%s\n", s.Name, s.State, s.Restarts, cpu, mem)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func (c *client) releaseLog(ctx context.Context, project, release string) error {
	resp, err := c.request(ctx, "GET", "/api/v1/projects/"+project+"/releases/"+url.PathEscape(release)+"/log", "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

// shortCommit is "3a77de6 feat: …" for the releases table.
func shortCommit(r api.ReleaseView) string {
	if r.Commit == "" {
		return "-"
	}
	sha, dirty := strings.CutSuffix(r.Commit, "-dirty")
	sha = sha[:min(len(sha), 7)]
	if dirty {
		sha += "*"
	}
	msg := []rune(r.CommitMessage)
	if len(msg) > 40 {
		msg = append(msg[:39], '…')
	}
	return strings.TrimSpace(sha + " " + string(msg))
}

func (c *client) listBackups(ctx context.Context, base string, remote bool) ([]deploy.Backup, error) {
	if remote {
		base += "?remote=1"
	}
	var bs []deploy.Backup
	err := c.do(ctx, "GET", base, nil, &bs)
	return bs, err
}

// restore replaces live data with a backup, after the owner types the project's name. "latest"
// restores the newest backup of every kind, which is how a rebuilt server gets its data back.
func (c *client) restore(ctx context.Context, project, name string, remote bool, from string) error {
	source := project
	if from != "" {
		source = from
	}
	names := []string{name}
	if name == "latest" {
		bs, err := c.listBackups(ctx, "/api/v1/projects/"+source+"/backups", remote)
		if err != nil {
			return err
		}
		names = newestPerKind(bs)
		if len(names) == 0 {
			return errors.New("no backups to restore")
		}
	}
	fmt.Printf("This replaces the live data of %s with:\n", project)
	for _, n := range names {
		fmt.Printf("  %s\n", n)
	}
	fmt.Print("A backup of what is replaced is taken first. Type the project name to confirm: ")
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(answer) != project {
		return errors.New("not confirmed; nothing restored")
	}
	for _, n := range names {
		body, _ := json.Marshal(map[string]any{"name": n, "from_project": from, "remote": remote, "confirm": project})
		if err := c.stream(ctx, "/api/v1/projects/"+project+"/backups/restore", "application/json", bytes.NewReader(body)); err != nil {
			return err
		}
	}
	return nil
}

// newestPerKind picks the newest backup of each kind (bs is newest first), leaving out
// Kayly's own, which restores a whole server.
func newestPerKind(bs []deploy.Backup) []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range bs {
		kind := b.Name[strings.IndexByte(b.Name, '.')+1:]
		if kind != "kayly.tar" && !seen[kind] {
			seen[kind] = true
			out = append(out, b.Name)
		}
	}
	return out
}

func (c *client) offsite(ctx context.Context, pos []string) error {
	const path = "/api/v1/offsite"
	switch {
	case pos[0] == "status" && len(pos) == 1:
		var st offsite.Status
		if err := c.do(ctx, "GET", path, nil, &st); err != nil {
			return err
		}
		if !st.Configured {
			fmt.Println("off-site backups are not set up: kayly backups setup <endpoint> <bucket> <access-key>")
			return nil
		}
		fmt.Printf("bucket %s at %s\nlast sync: %s\n", st.Bucket, st.Endpoint, st.LastSync.Format(time.RFC3339))
		if st.LastError != "" {
			fmt.Printf("last error: %s\n", st.LastError)
		}
		return nil
	case pos[0] == "sync" && len(pos) == 1:
		return c.do(ctx, "POST", path+"/sync", nil, nil)
	case pos[0] == "kayly" && len(pos) == 1:
		var b deploy.Backup
		if err := c.do(ctx, "POST", "/api/v1/backups/kayly", nil, &b); err != nil {
			return err
		}
		fmt.Printf("%s (%d KB)\n", b.Name, b.Size/1024)
		return nil
	case pos[0] == "drill" && len(pos) == 1:
		fmt.Println("restoring the newest off-site dump of every project into a throwaway database…")
		if err := c.do(ctx, "POST", path+"/drill", nil, nil); err != nil {
			return err
		}
		fmt.Println("drill passed")
		return nil
	case pos[0] == "setup" && (len(pos) == 4 || len(pos) == 5):
		secretKey, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if secretKey = strings.TrimSpace(secretKey); secretKey == "" {
			return fmt.Errorf("pipe the secret key on stdin (%v)", err)
		}
		body := map[string]string{"endpoint": pos[1], "bucket": pos[2], "access_key": pos[3], "secret_key": secretKey}
		if len(pos) == 5 {
			body["region"] = pos[4]
		}
		var out struct {
			RecoveryKey string `json:"recovery_key"`
		}
		if err := c.do(ctx, "PUT", path, body, &out); err != nil {
			return err
		}
		if out.RecoveryKey == "" {
			fmt.Println("bucket updated; the recovery key is unchanged")
			return nil
		}
		fmt.Printf("RECOVERY KEY (shown once; store it off this server, e.g. in a password manager):\n\n  %s\n\n"+
			"Backups in the bucket are encrypted with it. Without it they cannot be read; with it and the bucket,\n"+
			"anyone can read them.\n", out.RecoveryKey)
		return nil
	}
	return errors.New("usage: kayly backups setup <endpoint> <bucket> <access-key> [region] | status | sync")
}

func (c *client) domains(ctx context.Context, proj func(string) (string, error), pos []string) error {
	name, err := proj(".")
	if err != nil {
		return err
	}
	base := "/api/v1/projects/" + name + "/domains"
	switch {
	case len(pos) == 0 || pos[0] == "ls":
		var ds []api.DomainView
		if err := c.do(ctx, "GET", base, nil, &ds); err != nil {
			return err
		}
		for _, d := range ds {
			fmt.Printf("%s → %s\n", d.Hostname, d.Service)
		}
		return nil
	case pos[0] == "add" && len(pos) == 3:
		if err := c.do(ctx, "POST", base, api.DomainView{Hostname: pos[1], Service: pos[2]}, nil); err != nil {
			return err
		}
		fmt.Printf("%s → %s. Point an A record at this server; HTTPS follows once DNS resolves.\n", pos[1], pos[2])
		fmt.Println("Templates such as ${" + pos[2] + ".url} pick it up on the next deploy.")
		return nil
	case pos[0] == "rm" && len(pos) == 2:
		return c.do(ctx, "DELETE", base+"/"+pos[1], nil, nil)
	}
	return errors.New("usage: kayly domains ls | add <hostname> <service> | rm <hostname>")
}

func (c *client) audit(ctx context.Context, n int) error {
	var entries []api.AuditView
	if err := c.do(ctx, "GET", fmt.Sprintf("/api/v1/audit?limit=%d", n), nil, &entries); err != nil {
		return err
	}
	tw := table()
	fmt.Fprintln(tw, "WHEN\tTOKEN\tACTION\tPROJECT")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.At, e.Token, e.Action, e.Project)
	}
	return tw.Flush()
}

func (c *client) tokens(ctx context.Context, pos []string, scope, project string, expiresDays int) error {
	switch {
	case len(pos) == 2 && pos[0] == "create":
		var t api.TokenView
		if err := c.do(ctx, "POST", "/api/v1/tokens", map[string]any{"name": pos[1], "scope": scope, "project": project, "expires_days": expiresDays}, &t); err != nil {
			return err
		}
		fmt.Printf("%s\n\nShown once. On your laptop: kayly login https://<kayly host> %s\n", t.Token, t.Token)
		return nil
	case len(pos) == 1 && pos[0] == "ls":
		var ts []api.TokenView
		if err := c.do(ctx, "GET", "/api/v1/tokens", nil, &ts); err != nil {
			return err
		}
		tw := table()
		fmt.Fprintln(tw, "ID\tNAME\tSCOPE\tPROJECT\tEXPIRES\tLAST USED")
		for _, t := range ts {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Name, t.Scope, cmp.Or(t.Project, "all"), cmp.Or(t.ExpiresAt, "never"), cmp.Or(t.LastUsed, "never"))
		}
		return tw.Flush()
	case len(pos) == 2 && pos[0] == "rm":
		return c.do(ctx, "DELETE", "/api/v1/tokens/"+pos[1], nil, nil)
	}
	return errors.New("usage: kayly token create <name> | ls | rm <id>")
}

func (c *client) notify(ctx context.Context, pos []string) error {
	const path = "/api/v1/notifications"
	set := func(body map[string]string) error { return c.do(ctx, "PUT", path, body, nil) }
	switch {
	case len(pos) == 1 && pos[0] == "ls":
		var v api.NotificationsView
		if err := c.do(ctx, "GET", path, nil, &v); err != nil {
			return err
		}
		fmt.Printf("telegram: %v (chat %s)\nwebhook:  %v\n", v.Telegram, v.TelegramChat, v.Webhook)
		return nil
	case len(pos) == 2 && pos[0] == "telegram":
		token, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if token = strings.TrimSpace(token); token == "" {
			return fmt.Errorf("pipe the bot token on stdin (%v)", err)
		}
		return set(map[string]string{"telegram_token": token, "telegram_chat": pos[1]})
	case len(pos) == 2 && pos[0] == "webhook":
		return set(map[string]string{"webhook_url": pos[1]})
	case len(pos) == 2 && pos[0] == "off" && pos[1] == "telegram":
		return set(map[string]string{"telegram_token": "", "telegram_chat": ""})
	case len(pos) == 2 && pos[0] == "off" && pos[1] == "webhook":
		return set(map[string]string{"webhook_url": ""})
	case len(pos) == 1 && pos[0] == "test":
		return c.do(ctx, "POST", path+"/test", nil, nil)
	}
	return errors.New("usage: kayly notify ls | telegram <chat-id> | webhook <url> | off telegram|webhook | test")
}

// deploy packs dir, creates the project on first use and streams the deploy.
func (c *client) deploy(ctx context.Context, name, dir string) error {
	if _, err := os.Stat(filepath.Join(dir, config.FileName)); err != nil {
		return fmt.Errorf("no %s in %s: describe the app's services first (see ARCHITECTURE.md §5)", config.FileName, dir)
	}
	err := c.do(ctx, "POST", "/api/v1/projects", map[string]string{"name": name}, nil)
	var ae *apiError
	switch {
	case err == nil:
		fmt.Printf("created project %s\n", name)
	case errors.As(err, &ae) && ae.status == http.StatusConflict:
	default:
		return err
	}
	files, err := sourceFiles(dir)
	if err != nil {
		return err
	}
	fmt.Printf("uploading %d files\n", len(files))
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(pack(dir, files, pw)) }()
	return c.stream(ctx, "/api/v1/projects/"+name+"/deploys", "application/gzip", pr, commitHeaders(dir)...)
}

// stream posts body and prints an NDJSON operation stream; a failed result is an error.
func (c *client) stream(ctx context.Context, path, contentType string, body io.Reader, headers ...string) error {
	resp, err := c.request(ctx, "POST", path, contentType, body, headers...)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e api.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return fmt.Errorf("bad event from server: %w", err)
		}
		if e.Result != nil {
			if !e.Result.OK {
				return errors.New(e.Result.Error)
			}
			return nil
		}
		fmt.Print(e.Log)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("the connection ended before the result; check `kayly releases`")
}

// client calls the daemon: through the admin socket on the server, otherwise over HTTPS
// with the token saved by `kayly login`.
type client struct {
	base  string
	token string
	http  *http.Client
}

type savedLogin struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func loginFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kayly", "login.json"), nil
}

func newClient() (*client, error) {
	if u, t := os.Getenv("KAYLY_URL"), os.Getenv("KAYLY_TOKEN"); u != "" {
		return &client{base: strings.TrimSuffix(u, "/"), token: t, http: &http.Client{}}, nil
	}
	run := os.Getenv("KAYLY_RUN")
	if run == "" {
		run = "/run/kayly"
	}
	sock := adminSock(run)
	if _, err := os.Stat(sock); err == nil {
		return &client{base: "http://kayly", http: &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		}}}, nil
	}
	file, err := loginFile()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, errors.New("not logged in: run `kayly login <url> <token>` (create the token on the server with `kayly token create <name>`)")
	}
	var l savedLogin
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return &client{base: l.URL, token: l.Token, http: &http.Client{}}, nil
}

func login(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: kayly login <url> <token>")
	}
	u := strings.TrimSuffix(args[0], "/")
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		u = "https://" + u
	}
	c := &client{base: u, token: args[1], http: &http.Client{}}
	if err := c.do(context.Background(), "GET", "/api/v1/projects", nil, nil); err != nil {
		return err
	}
	file, err := loginFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(savedLogin{URL: u, Token: args[1]})
	if err := os.WriteFile(file, b, 0o600); err != nil {
		return err
	}
	fmt.Printf("logged in to %s\n", u)
	return nil
}

type apiError struct {
	status    int
	msg, hint string
}

func (e *apiError) Error() string { return e.msg }

func (c *client) request(ctx context.Context, method, path, contentType string, body io.Reader, headers ...string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var eb api.ErrorBody
		if json.NewDecoder(resp.Body).Decode(&eb) != nil || eb.Error.Message == "" {
			return nil, &apiError{status: resp.StatusCode, msg: resp.Status}
		}
		return nil, &apiError{status: resp.StatusCode, msg: eb.Error.Message, hint: eb.Error.Hint}
	}
	return resp, nil
}

// do sends in as JSON (if not nil) and decodes the answer into out (if not nil).
func (c *client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	ct := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, ct = strings.NewReader(string(b)), "application/json"
	}
	resp, err := c.request(ctx, method, path, ct, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func table() *tabwriter.Writer { return tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0) }

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
