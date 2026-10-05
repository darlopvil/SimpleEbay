# SimpleEbay

A privacy-friendly frontend for eBay. Search and browse listings without
JavaScript, without trackers and without your browser ever talking to eBay.

Unlike scraping frontends, SimpleEbay uses eBay's official
[Browse API](https://developer.ebay.com/api-docs/buy/browse/overview.html), so
it does not break when the site's HTML changes and it is not affected by
anti-bot blocking.

> **Status:** early development. Search works; item pages are next.

## Requirements

You need your **own** eBay developer keyset. Application keys cannot be shared
with third parties under eBay's API License Agreement, so every instance must
bring its own.

1. Sign up at [developer.ebay.com](https://developer.ebay.com/) and create a
   **Production** keyset.
2. Under *Notifications → Marketplace Account Deletion*, either subscribe to
   the notifications or, since SimpleEbay only keeps data in a short-lived
   in-memory cache, opt out with *Not persisting eBay data*. The keyset is not
   activated until you do one of the two.

The default quota for the Browse API is 5,000 calls per day per keyset.
SimpleEbay is meant for **personal instances**: put it behind authentication
if it is reachable from the internet, or crawlers will burn your quota.

## Running it

```bash
cp .env.example .env && chmod 600 .env   # fill in your keyset
cp docker-compose.example.yml docker-compose.yml
docker compose up -d --build
```

The image is a `scratch` container with a single static binary; templates and
static files are embedded.

### Configuration

| Flag | Environment | Default | Meaning |
| --- | --- | --- | --- |
| | `EBAY_CLIENT_ID` | | App ID (Client ID) of your keyset |
| | `EBAY_CLIENT_SECRET` | | Cert ID (Client Secret) of your keyset |
| | `SIMPLEEBAY_ENVIO_PAIS` | | Destination country for shipping estimates |
| | `SIMPLEEBAY_ENVIO_CP` | | Destination postal code for shipping estimates |
| | `TZ` | `UTC` | Time zone for auction end times, e.g. `Europe/Madrid` |
| `-h` | `SIMPLEEBAY_HOST` | `0.0.0.0` | Listen address |
| `-p` | `SIMPLEEBAY_PORT` | `8080` | Listen port |
| `-probe` | | | Print the raw JSON of an API path and exit |
| `-mp` | | `EBAY_ES` | Marketplace used by `-probe` |

## Development notes

- Probe the real API response before mapping any field:
  `simpleebay -probe '/buy/browse/v1/item_summary/search?q=game+boy&limit=1'`
- Inside the container: `docker exec simpleebay_app /simpleebay -probe '...'`

## License

AGPL-3.0-or-later.
