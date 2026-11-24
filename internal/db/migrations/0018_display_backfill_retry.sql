-- The first backfill of display renditions (0017) ran with a scaler that
-- needed more memory than the server has for any image above the display
-- budget: the server was killed part-way through such an image, after the
-- image had been marked as decided. Heavy images that came out of that pass
-- without a rendition are offered to the backfill once more.
UPDATE attachments
SET display_checked_at = NULL
WHERE kind = 'image'
  AND display_key IS NULL
  AND display_checked_at IS NOT NULL
  AND content_type <> 'image/gif'
  AND size > 524288;
