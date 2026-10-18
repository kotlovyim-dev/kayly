package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kotlovyim-dev/kayly/internal/offsite"
)

// restoreServer is `kayly restore`: it runs on a fresh host before the daemon starts and
// brings back the database, the master key and the newest backups from the bucket.
func restoreServer(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	data := fs.String("data", "/var/lib/kayly", "state directory")
	endpoint := fs.String("endpoint", "", "S3 endpoint, e.g. fsn1.your-objectstorage.com")
	region := fs.String("region", "", "S3 region (optional)")
	bucket := fs.String("bucket", "", "bucket name")
	accessKey := fs.String("access-key", "", "S3 access key; the secret key is read from stdin")
	recovery := fs.String("recovery-key", "", "the recovery key printed by `kayly backups setup`")
	fs.Parse(args)
	if *endpoint == "" || *bucket == "" || *accessKey == "" || *recovery == "" {
		return errors.New("usage: kayly restore -endpoint E -bucket B -access-key K -recovery-key KEY [-region R] < secret-key-file")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(*recovery))
	if err != nil || len(key) != offsite.KeySize {
		return errors.New("the recovery key is not valid: copy it exactly as `kayly backups setup` printed it")
	}
	secretKey, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if secretKey = strings.TrimSpace(secretKey); secretKey == "" {
		return fmt.Errorf("pipe the S3 secret key on stdin (%v)", err)
	}
	b, err := offsite.OpenS3(offsite.Config{Endpoint: *endpoint, Region: *region, Bucket: *bucket, AccessKey: *accessKey, SecretKey: secretKey})
	if err != nil {
		return err
	}
	say := func(format string, args ...any) { fmt.Printf("  "+format+"\n", args...) }
	fmt.Println("restoring from the bucket:")
	if err := offsite.RestoreServer(context.Background(), b, key, *data, filepath.Join(*data, "backups"), say); err != nil {
		return err
	}
	fmt.Print(`
Done. Next:
  1. systemctl start kayly        (it recreates the add-ons, empty)
  2. kayly deploy -p <project>    from each app's working tree, or push the repo
  3. kayly backups restore latest -p <project>    for every project with data
`)
	return nil
}
