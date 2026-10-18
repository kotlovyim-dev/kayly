package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/kotlovyim-dev/kayly/internal/config"
	"github.com/kotlovyim-dev/kayly/internal/store"
	"github.com/kotlovyim-dev/kayly/internal/telegram"
)

// bots returns the bot services of cfg, by name.
func bots(cfg *config.Config) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(cfg.Services)) {
		if cfg.Services[name].Telegram != nil {
			out = append(out, name)
		}
	}
	return out
}

// bot is the Bot API client of a bot service, with the token rel delivers to it.
func (d *Deployer) bot(rel store.Release, service string, tg *config.Telegram) telegram.Bot {
	return telegram.Bot{API: d.opt.TelegramAPI, Token: rel.Env[service][tg.Token]}
}

// prepareBots runs before rel starts and before anything live changes: every bot's token
// must work, a webhook bot must have a URL, and a polling bot gets its webhook removed so
// its getUpdates works at once.
func (d *Deployer) prepareBots(ctx context.Context, p store.Project, rel store.Release, cfg *config.Config, w io.Writer) error {
	names := bots(cfg)
	if len(names) == 0 {
		return nil
	}
	step(w, "telegram")
	domains, err := d.store.Domains(ctx, p.ID)
	if err != nil {
		return err
	}
	for _, name := range names {
		tg := cfg.Services[name].Telegram
		b := d.bot(rel, name, tg)
		user, err := b.Username(ctx)
		if err != nil {
			return fmt.Errorf("%s: the token in %s does not work: %w", name, tg.Token, err)
		}
		fmt.Fprintf(w, "%s is @%s (%s)\n", name, user, tg.Mode)
		if tg.Mode == "webhook" {
			if _, err := d.webhookURL(p.Name, name, tg, cfg, domains); err != nil {
				return err
			}
			continue
		}
		if have, err := b.WebhookURL(ctx); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		} else if have != "" {
			fmt.Fprintf(w, "%s: removing the webhook %s for polling\n", name, have)
			if err := b.DeleteWebhook(ctx); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	return nil
}

func (d *Deployer) webhookURL(project, service string, tg *config.Telegram, cfg *config.Config, domains []store.Domain) (string, error) {
	base, ok := d.serviceURLs(project, cfg, domains)[service+".url"]
	if !ok {
		return "", fmt.Errorf("%s: a webhook bot needs an HTTPS address; add one with `kayly domains add <hostname> %s -p %s`", service, service, project)
	}
	return base + tg.Path, nil
}

// syncBots points every bot of the live release rel at what its telegram table wants:
// webhook or none, and the menu button. Each is read first and set only if it differs, so an
// ordinary deploy never touches Telegram. prevEnv is the env of the release rel replaced
// (nil if none): Telegram never reports a webhook's secret, so a changed secret is noticed
// there. The owner hears once when a bot starts failing and once when it recovers.
func (d *Deployer) syncBots(ctx context.Context, p store.Project, rel store.Release, cfg *config.Config, prevEnv map[string]map[string]string) error {
	names := bots(cfg)
	if len(names) == 0 {
		return nil
	}
	domains, err := d.store.Domains(ctx, p.ID)
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range names {
		err := d.syncBot(ctx, p, rel, name, cfg, domains, prevEnv[name][config.WebhookSecretKey])
		key := p.Name + "/" + name
		d.botMu.Lock()
		told := d.botFailing[key]
		d.botFailing[key] = err != nil
		d.botMu.Unlock()
		switch {
		case err != nil && !told:
			d.log.Warn("telegram setup failed", "project", p.Name, "service", name, "err", err)
			d.alert(ctx, "✗ %s: Telegram setup of %s failed: %v\nr%d is live; Kayly retries every 30 s.", p.Name, name, err, rel.Seq)
		case err == nil && told:
			d.alert(ctx, "✓ %s: Telegram setup of %s works again", p.Name, name)
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (d *Deployer) syncBot(ctx context.Context, p store.Project, rel store.Release, name string, cfg *config.Config, domains []store.Domain, prevSecret string) error {
	tg := cfg.Services[name].Telegram
	b := d.bot(rel, name, tg)
	have, err := b.WebhookURL(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	switch tg.Mode {
	case "webhook":
		want, err := d.webhookURL(p.Name, name, tg, cfg, domains)
		if err != nil {
			return err
		}
		secret := rel.Env[name][config.WebhookSecretKey]
		if have != want || secret != prevSecret {
			if err := b.SetWebhook(ctx, want, secret); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			d.log.Info("telegram webhook set", "project", p.Name, "service", name, "url", want)
		}
	case "polling":
		// A webhook set by hand, or by a rollback racing a deploy, would starve getUpdates.
		if have != "" {
			if err := b.DeleteWebhook(ctx); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	m := tg.MenuButton
	if m == nil {
		return nil
	}
	url, err := config.ExpandRefs(m.URL, d.serviceURLs(p.Name, cfg, domains))
	if err != nil {
		return fmt.Errorf("%s: menu button: %w", name, err)
	}
	want := telegram.MenuButton{Text: m.Text, URL: url}
	if cur, err := b.MenuButton(ctx); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	} else if cur != want {
		if err := b.SetMenuButton(ctx, want); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}
