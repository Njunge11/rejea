package main

import (
	"context"
	"errors"
	"log/slog"
	"syscall"

	"github.com/Njunge11/rejea/internal/chat"
	"github.com/Njunge11/rejea/internal/config"
	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/embed"
	"github.com/Njunge11/rejea/internal/parse"
	"github.com/Njunge11/rejea/internal/rerank"
	"github.com/Njunge11/rejea/internal/storage"
)

// errSettings is the error of a setting that is missing or wrong.
var errSettings = errors.New("the settings are not correct")

// advice tells the developer what to do about an error that stops a
// command. It names the settings and the commands of this repository, which
// the parts of the service do not know. It gives "" for an error that has no
// known correction.
func advice(err error) string {
	switch {
	case errors.Is(err, errSettings):
		return `Each setting and its default is in .env.example. Copy that file to .env and change the value there. "make up" needs no settings file.`

	case errors.Is(err, database.ErrURL):
		return `Give DATABASE_URL in .env the form postgres://user:password@host:port/database. The value for the database of compose.yaml is in .env.example.`
	case errors.Is(err, database.ErrNoAnswer):
		return `Start the database with "make db-up". If your database is at a different address, change DATABASE_URL in .env.`
	case errors.Is(err, database.ErrRefused):
		return `Give DATABASE_URL in .env the user and the password of the database. For the database of compose.yaml, each of the two is "rejea".`
	case errors.Is(err, database.ErrNoDatabase):
		return `Give DATABASE_URL in .env the name of a database that the server has. The database of compose.yaml has the name "rejea".`
	case errors.Is(err, database.ErrNotMigrated):
		return `Run "make migrate".`
	case errors.Is(err, database.ErrNewerSchema):
		return `Get the newest code of the service ("git pull"): a newer version migrated this database.`

	case errors.Is(err, storage.ErrNotSet):
		return `Set REJEA_S3_BUCKET and REJEA_S3_REGION in .env. The values for the file store of compose.yaml are in .env.example.`
	case errors.Is(err, storage.ErrEndpoint):
		return `Correct REJEA_S3_ENDPOINT and REJEA_S3_PUBLIC_ENDPOINT in .env. For AWS S3, remove the two.`
	case errors.Is(err, storage.ErrNoCredentials):
		return `Set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY in .env to the two keys of the file store: the values for the store of compose.yaml are in .env.example. For AWS S3, set AWS_PROFILE or give the machine a role: see "File storage" in the README.`
	case errors.Is(err, storage.ErrNoAnswer):
		return `Start the file store with "make db-up". If your store is at a different address, change REJEA_S3_ENDPOINT in .env. For AWS S3, examine the network connection.`
	case errors.Is(err, storage.ErrRefused):
		return `Give AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY in .env the two keys of the file store: for the store of compose.yaml, the values of REJEA_STORAGE_ACCESS_KEY and REJEA_STORAGE_SECRET_KEY. For AWS S3, the role needs s3:ListBucket on the bucket: see "File storage" in the README.`
	case errors.Is(err, storage.ErrNoBucket):
		return `Give REJEA_S3_BUCKET in .env the name of a bucket that the store has. The bucket of the store of compose.yaml has the name "rejea".`
	case errors.Is(err, storage.ErrViewSettings):
		return `Set REJEA_CLOUDFRONT_DOMAIN, REJEA_CLOUDFRONT_KEY_PAIR_ID and REJEA_CLOUDFRONT_PRIVATE_KEY_FILE in .env, or remove the three: with no values, the URL of a page image goes to the bucket. See "Pages of a document" in the README.`
	case errors.Is(err, storage.ErrViewDomain):
		return `Give REJEA_CLOUDFRONT_DOMAIN in .env the domain name of the distribution only, with no "https://" and no path.`
	case errors.Is(err, storage.ErrViewKey):
		return `Give REJEA_CLOUDFRONT_PRIVATE_KEY_FILE in .env the path of the private key of the public key in REJEA_CLOUDFRONT_KEY_PAIR_ID: an RSA key of 2,048 bits in a PEM file. To make a key: "openssl genrsa -out private_key.pem 2048". See "Pages of a document" in the README.`
	case errors.Is(err, storage.ErrViewNoAnswer):
		return `Give REJEA_CLOUDFRONT_DOMAIN in .env the domain name of your distribution: the CloudFront console shows it as "Distribution domain name".`
	case errors.Is(err, storage.ErrViewKeyPair):
		return `Give REJEA_CLOUDFRONT_KEY_PAIR_ID in .env the ID of your public key: the CloudFront console shows it in "Key management", "Public keys". The key must be in a key group that the distribution trusts.`
	case errors.Is(err, storage.ErrViewSignature):
		return `Give REJEA_CLOUDFRONT_PRIVATE_KEY_FILE in .env the private key of the public key in REJEA_CLOUDFRONT_KEY_PAIR_ID: the file has a different key now. If the key is correct, set the clock of this machine.`
	case errors.Is(err, storage.ErrRegion):
		return `Give REJEA_S3_REGION in .env the AWS Region of the bucket.`

	case errors.Is(err, errDimensions):
		return `Give REJEA_EMBED_DIMENSIONS and REJEA_EMBED_MODEL in .env a model with the dimensions of the column. For a model with a different number, change the column chunks.embedding with a migration first.`
	case errors.Is(err, embed.ErrRefused):
		return `Give REJEA_EMBED_API_KEY and CLOUDFLARE_ACCOUNT_ID in .env the API token and the account ID of your Cloudflare account: see "Embeddings" in the README. The token needs the permissions "Workers AI Read" and "Workers AI Edit".`
	case errors.Is(err, embed.ErrNoModel):
		return `Give REJEA_EMBED_MODEL in .env the name of an embedding model of the provider. The default is @cf/qwen/qwen3-embedding-0.6b.`
	case errors.Is(err, embed.ErrAnswer):
		return `Give REJEA_EMBED_DIMENSIONS in .env the number of dimensions of the model in REJEA_EMBED_MODEL. The column chunks.embedding has 1,024 dimensions: a model with a different number also needs a migration.`
	case errors.Is(err, rerank.ErrRefused):
		return `Give REJEA_RERANK_API_KEY in .env an API key of the provider in REJEA_RERANK_PROVIDER. A key of a different provider does not work. The provider cloudflare needs CLOUDFLARE_ACCOUNT_ID also. See "Reranker" in the README.`
	case errors.Is(err, rerank.ErrNoModel):
		return `Give REJEA_RERANK_MODEL in .env the name of a reranker model of the provider in REJEA_RERANK_PROVIDER, or remove the setting: each provider has a default model. See "Reranker" in the README.`
	case errors.Is(err, rerank.ErrInput), errors.Is(err, rerank.ErrAnswer):
		return `Give REJEA_RERANK_MODEL in .env the name of a reranker model of the provider in REJEA_RERANK_PROVIDER, or remove the setting: a model of a different type does not give scores. See "Reranker" in the README.`
	case errors.Is(err, chat.ErrRefused):
		return `Give REJEA_CHAT_API_KEY in .env an API key of the provider in REJEA_CHAT_PROVIDER. A key of a different provider does not work. See "Chat model" in the README.`
	case errors.Is(err, chat.ErrNoModel):
		return `Give REJEA_CHAT_MODEL in .env the name of a chat model of the provider in REJEA_CHAT_PROVIDER, or remove the setting: each provider has a default model. See "Chat model" in the README.`

	case errors.Is(err, syscall.EADDRINUSE):
		return `Stop the other program that uses this port, or change the port in REJEA_HTTP_ADDR in .env.`
	}
	return ""
}

// warnOfMissingReaders logs a warning for the parser and for the Word
// reader when one does not answer. The service starts all the same: a file
// that needs the reader waits, and the service tries again.
func warnOfMissingReaders(ctx context.Context, logger *slog.Logger, cfg config.Config, parser *parse.PaddleOCR, words *parse.Word) {
	ctx, cancel := context.WithTimeout(ctx, cfg.StartCheckTimeout)
	defer cancel()
	if err := parser.Ready(ctx); err != nil {
		logger.WarnContext(ctx, "the parser does not answer: a PDF waits until it does",
			"url", cfg.Parse.URL,
			"to_correct", `start the parser with "make parser" ("make up" starts it on a Mac with Apple Silicon)`,
			"err", err)
	}
	if _, err := words.Name(ctx); err != nil {
		logger.WarnContext(ctx, "the Word reader does not run: a Word file waits until it does",
			"path", cfg.Word.Path,
			"to_correct", `install the Word reader with "make word-install"`,
			"err", err)
	}
}
