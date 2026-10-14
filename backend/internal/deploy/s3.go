package deploy

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/secrets"
)

// S3 storage (Settings → Backup storage) is used through the aws-cli image on
// the server holding the file, so PortDock needs no SDK and any S3-compatible
// service works (AWS, R2, B2, MinIO). The credentials go through a 0600 env
// file, not the command line, where `ps` would show them.

const awsImage = "amazon/aws-cli"

type s3Config struct {
	endpoint, bucket, region, key, secret string
}

// S3Configured reports whether backup storage is set up.
func (e *Engine) S3Configured(ctx context.Context) bool {
	_, err := e.s3Config(ctx)
	return err == nil
}

func (e *Engine) s3Config(ctx context.Context) (s3Config, error) {
	c := s3Config{
		endpoint: e.db.Setting(ctx, "s3_endpoint"),
		bucket:   e.db.Setting(ctx, "s3_bucket"),
		region:   e.db.Setting(ctx, "s3_region"),
		// Both credentials are stored encrypted (values saved before v1.12 are plain text).
		key:    e.box.MustDecrypt(e.db.Setting(ctx, "s3_access_key")),
		secret: e.box.MustDecrypt(e.db.Setting(ctx, "s3_secret_key")),
	}
	if c.bucket == "" || c.key == "" || c.secret == "" {
		return c, errors.New("S3 storage is not configured (Settings → Backup storage)")
	}
	if c.region == "" {
		c.region = "us-east-1"
	}
	return c, nil
}

// awsCLI runs `aws <args>` with dir mounted at /b. The env file holding the
// credentials is written next to the data and removed afterwards.
func (e *Engine) awsCLI(ctx context.Context, x executor.Executor, c s3Config, dir string, readOnly bool, args string) (string, error) {
	envPath := path.Join(e.cfg.DataDir, ".s3-"+secrets.RandomToken(8)+".env")
	env := fmt.Sprintf("AWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\nAWS_DEFAULT_REGION=%s\n", c.key, c.secret, c.region)
	if err := executor.WriteFile(ctx, x, envPath, []byte(env), "0600"); err != nil {
		return "", err
	}
	defer func() { _ = x.Run(context.WithoutCancel(ctx), "rm -f "+q(envPath), executor.RunOpts{}) }()
	mount := ""
	if dir != "" {
		mount = " -v " + q(dir+":/b")
		if readOnly {
			mount += ":ro"
		}
	}
	cmd := fmt.Sprintf("docker run --rm --env-file %s%s %s %s", q(envPath), mount, awsImage, args)
	if c.endpoint != "" {
		cmd += " --endpoint-url " + q(c.endpoint)
	}
	return executor.Output(ctx, x, cmd)
}

// s3URL is the address of name under the PortDock prefix of the bucket.
func (c s3Config) url(prefix, name string) string {
	return "s3://" + c.bucket + "/portdock/" + prefix + "/" + name
}

func (e *Engine) uploadS3(ctx context.Context, x executor.Executor, file, prefix string) error {
	c, err := e.s3Config(ctx)
	if err != nil {
		return err
	}
	dir, name := path.Split(file)
	_, err = e.awsCLI(ctx, x, c, strings.TrimSuffix(dir, "/"), true, "s3 cp "+q("/b/"+name)+" "+q(c.url(prefix, name)))
	return err
}

// UploadS3 copies a file of the PortDock container to the bucket, under portdock/<prefix>/.
func (e *Engine) UploadS3(ctx context.Context, file, prefix string) error {
	return e.uploadS3(ctx, executor.Local{}, file, prefix)
}

// DownloadS3 copies portdock/<prefix>/<name> from the bucket into dir (in the PortDock container).
func (e *Engine) DownloadS3(ctx context.Context, prefix, name, dir string) error {
	c, err := e.s3Config(ctx)
	if err != nil {
		return err
	}
	if name == "" || strings.ContainsAny(name, "/\\") {
		return errors.New("invalid file name")
	}
	_, err = e.awsCLI(ctx, executor.Local{}, c, dir, false, "s3 cp "+q(c.url(prefix, name))+" "+q("/b/"+name))
	return err
}

// S3Object is a file of the bucket under portdock/<prefix>/.
type S3Object struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

// ListS3 lists the files under portdock/<prefix>/, oldest first.
func (e *Engine) ListS3(ctx context.Context, prefix string) ([]S3Object, error) {
	c, err := e.s3Config(ctx)
	if err != nil {
		return nil, err
	}
	out, err := e.awsCLI(ctx, executor.Local{}, c, "", false, "s3 ls "+q(c.url(prefix, "")))
	if err != nil {
		// An empty prefix makes `aws s3 ls` exit 1 with no output.
		if strings.TrimSpace(out) == "" && strings.Contains(err.Error(), "exit status 1") {
			return []S3Object{}, nil
		}
		return nil, err
	}
	return parseS3List(out), nil
}

// parseS3List reads `aws s3 ls` lines: "2026-10-12 03:00:01    1048576 name".
func parseS3List(out string) []S3Object {
	objs := []S3Object{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 4 || f[0] == "PRE" {
			continue
		}
		size, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			continue
		}
		objs = append(objs, S3Object{Name: strings.Join(f[3:], " "), Size: size, Modified: f[0] + " " + f[1]})
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].Name < objs[j].Name })
	return objs
}

// DeleteS3 removes portdock/<prefix>/<name> from the bucket.
func (e *Engine) DeleteS3(ctx context.Context, prefix, name string) error {
	c, err := e.s3Config(ctx)
	if err != nil {
		return err
	}
	if name == "" || strings.ContainsAny(name, "/\\") {
		return errors.New("invalid file name")
	}
	_, err = e.awsCLI(ctx, executor.Local{}, c, "", false, "s3 rm "+q(c.url(prefix, name)))
	return err
}
