// Command kayly is both the Kayly daemon (`kayly serve`) and its CLI.
package main

import (
	"bufio"
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

	"github.com/kotlovyim-dev/kayly/internal/api"
	"github.com/kotlovyim-dev/kayly/internal/config"
)

const usage = `kayly deploys apps to your own server.

On the server:
  kayly serve --base-domain <ip>.sslip.io   run the daemon (installed as a systemd unit)
  kayly token create <name>                 create an API token for a laptop or CI
  kayly token ls | token rm <id>

Anywhere:
  kayly login <url> <token>                 remember a server
  kayly deploy [dir] [-p project]           build and release dir (default .)
  kayly projects                            list projects
  kayly releases [-p project]               list releases
  kayly rollback <release> [-p project]     make an earlier release live again
  kayly logs <service> [-p project] [-f] [-n lines]
  kayly env ls | set KEY=value... | rm KEY... [-p project]
  kayly domains ls | add <hostname> <service> | rm <hostname> [-p project]

The project defaults to the directory name. On the server, the CLI talks to the daemon's
admin socket (in $KAYLY_RUN, default /run/kayly) and needs no login.
`

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
	case "releases":
		name, err := proj(".")
		if err != nil {
			return err
		}
		var rs []api.ReleaseView
		if err := c.do(ctx, "GET", "/api/v1/projects/"+name+"/releases", nil, &rs); err != nil {
			return err
		}
		tw := table()
		fmt.Fprintln(tw, "RELEASE\tSTATUS\tCREATED\tERROR")
		for _, r := range rs {
			fmt.Fprintf(tw, "r%d\t%s\t%s\t%s\n", r.Seq, r.Status, r.CreatedAt, firstLine(r.Error))
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
	case "env":
		return c.env(ctx, proj, pos)
	case "domains":
		return c.domains(ctx, proj, pos)
	case "token":
		return c.tokens(ctx, pos)
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

func (c *client) tokens(ctx context.Context, pos []string) error {
	switch {
	case len(pos) == 2 && pos[0] == "create":
		var t api.TokenView
		if err := c.do(ctx, "POST", "/api/v1/tokens", map[string]string{"name": pos[1]}, &t); err != nil {
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
		fmt.Fprintln(tw, "ID\tNAME\tCREATED")
		for _, t := range ts {
			fmt.Fprintf(tw, "%d\t%s\t%s\n", t.ID, t.Name, t.CreatedAt)
		}
		return tw.Flush()
	case len(pos) == 2 && pos[0] == "rm":
		return c.do(ctx, "DELETE", "/api/v1/tokens/"+pos[1], nil, nil)
	}
	return errors.New("usage: kayly token create <name> | ls | rm <id>")
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
	return c.stream(ctx, "/api/v1/projects/"+name+"/deploys", "application/gzip", pr)
}

// stream posts body and prints an NDJSON operation stream; a failed result is an error.
func (c *client) stream(ctx context.Context, path, contentType string, body io.Reader) error {
	resp, err := c.request(ctx, "POST", path, contentType, body)
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

func (c *client) request(ctx context.Context, method, path, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
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
