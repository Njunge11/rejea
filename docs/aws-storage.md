# File storage in AWS

`make up` uses a file store in a container. This page is for a bucket in AWS S3, with or without CloudFront.

## A bucket in AWS S3

- Set `REJEA_S3_BUCKET` and `REJEA_S3_REGION` in `.env`, and remove `REJEA_S3_ENDPOINT`, `REJEA_S3_PUBLIC_ENDPOINT` and the two AWS keys.
- Run the service on the machine itself (see "Develop" in the README). The containers of `make up` use the store of `compose.yaml`.
- The service gets its AWS credentials from the default sources of the AWS SDK. Give it an IAM role, not the access key of a user.
- The role needs five actions, and no more:
  - `s3:GetObject` and `s3:PutObject` on the objects of the bucket.
  - `s3:DeleteObject` and `s3:AbortMultipartUpload` on the objects of the bucket. The service sends each file from an archive to the bucket, a large file in parts. When the work on an archive fails, the service removes what it sent.
  - `s3:ListBucket` on the bucket. With it, S3 answers 404 for a file that is not there. Without it, S3 answers 403, and the service cannot tell a missing file from a refused request.
- Give the bucket a lifecycle rule that removes incomplete multipart uploads. If the service stops in the middle of an upload in parts, the parts stay in the bucket until this rule removes them.
- On a developer machine, make an AWS profile that assumes the role, and name it in `AWS_PROFILE`:

```ini
[profile rejea]
role_arn = arn:aws:iam::<account id>:role/<role name>
source_profile = <a profile that can assume the role>
region = <the region of the bucket>
duration_seconds = 43200
```

S3 refuses an upload URL when the credentials that signed it are expired. Thus the session (`duration_seconds`) must be longer than `REJEA_UPLOAD_URL_LIFE`. The service does not start if it is not.

## CloudFront for the page images

With the store of `make up`, the URL of a page image is a presigned URL of the bucket. For a bucket in AWS S3 behind a CloudFront distribution, it is a signed CloudFront URL:

1. Make an RSA key pair of 2,048 bits: `openssl genrsa -out private_key.pem 2048`, then `openssl rsa -pubout -in private_key.pem -out public_key.pem`.
2. In CloudFront, add the public key ("Key management", "Public keys"), put it in a key group, and make that group a trusted key group of the behaviour of the distribution. The origin is the bucket, with origin access control.
3. Put these lines in `.env`, and run the service on the machine itself (see "Develop" in the README):

```sh
REJEA_CLOUDFRONT_DOMAIN=d111111abcdef8.cloudfront.net
REJEA_CLOUDFRONT_KEY_PAIR_ID=K2JCJMDEHXQW5F
REJEA_CLOUDFRONT_PRIVATE_KEY_FILE=/path/to/private_key.pem
```

The URLs of one list have one signature: a [custom policy](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/private-content-signed-urls.html) for the folder of the page images of the document, which the CloudFront documentation gives for more than one file. The signature uses SHA-256. It permits a read of the page images of that document only: CloudFront refuses the same signature for a different file.

When the service starts, it sends one signed request to the distribution. With a key pair ID that CloudFront does not know, a private key that is not the key of that ID, or a domain with no distribution, it stops and names the setting to correct.
