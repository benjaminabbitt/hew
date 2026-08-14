resource "aws_s3_bucket" "logs" {
  bucket = "example-logs"
  acl    = "private"
}

resource "aws_s3_bucket" "assets" {
  bucket = "example-assets"
  acl    = "private"
}
