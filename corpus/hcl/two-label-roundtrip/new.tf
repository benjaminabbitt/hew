resource "aws_s3_bucket" "logs" {
  bucket = "example-logs"
  acl    = "log-delivery-write"
}

resource "aws_s3_bucket" "assets" {
  bucket = "example-assets"
  acl    = "public-read"
}
