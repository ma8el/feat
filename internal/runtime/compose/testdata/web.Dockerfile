# The second repository's image.
#
# It copies the origin.txt of whichever directory it was built from, which is
# what makes the build context observable from inside the container: a build run
# against the first repository's directory produces an image that says so.
FROM alpine:3.20
COPY origin.txt /origin.txt
