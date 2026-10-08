# SimpleEbay

A privacy-friendly frontend for eBay. Search and browse listings without
JavaScript, without trackers and without your browser ever talking to eBay.

Unlike scraping frontends, SimpleEbay uses eBay's official
[Browse API](https://developer.ebay.com/api-docs/buy/browse/overview.html), so
it does not break when the site's HTML changes and it is not affected by
anti-bot blocking.

The interface is in Spanish.

## Features

- **Search** on any of the 16 marketplaces the Browse API supports, with
  filters for sort order, condition, format (buy it now or auction), item
  location (a country or the European Union) and a price range that includes
  shipping. Sorting by distance is offered when a postal code is configured.
- **Categories.** Browse a marketplace's categories from the home page, or
  narrow a search to one of them; results show how many listings each
  subcategory has.
- **Item specifics filters** (platform, region, brand, size…) for the
  category of the results, as checkboxes.
- **Seller listings.** Every seller name links to that seller's listings,
  which can be searched and filtered like any other results.
- **Compare with other sellers.** Listings tied to an eBay catalogue product
  link to every other listing of the same product.
- **Search by photo** on the marketplaces where eBay offers it (Germany, the
  UK, the US and Australia). The photo is sent to eBay and not stored.
- **Share links.** `ebay.io/m/{code}` short links from eBay's Share button
  open at `/m/{code}`; tracking parameters that identify who shared them are
  dropped.
- **Prices in euros** everywhere. When eBay does not already give the amount
  in euros, it is converted with the European Central Bank's daily reference
  rates and marked with `≈`, with the marketplace's and the seller's original
  prices shown underneath.
- **Item pages** at `/itm/{id}`, the same ID eBay uses in its URLs: photo
  gallery, price and discounts, auction details, variations (colour, size…),
  condition with the seller's notes, every shipping option with import
  charges and delivery estimates, returns, payment methods, seller details
  (legal information folded away), item specifics and the seller's full
  description.
- **Sanitised descriptions.** Seller HTML is stripped of scripts, external
  stylesheets, forms and hidden blocks, filtered through an allowlist, and
  links to other eBay listings are rewritten to SimpleEbay pages.
- **Every image goes through the server.** eBay photos are proxied under
  `/img/`; images that sellers host elsewhere are proxied under `/ext`, which
  only accepts URLs signed by the server itself and never connects to private,
  loopback or link-local addresses.
- **Per-IP rate limits** on everything that spends API quota, and on photo
  uploads, which must also come from the instance's own form.
- **No JavaScript at all**, a strict Content Security Policy, no third-party
  requests, light and dark themes, and a layout that works on phones.
- **Responses cached in memory** for ten minutes, and results fetched in
  blocks of three pages per API call, so paging through results and going
  back costs no quota. Category trees come from the Taxonomy API, are kept on
  the server and renewed once a day.

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
SimpleEbay is meant for **personal instances**. If yours is reachable from the
internet, its subdomain will show up in public certificate logs and every
crawler visit spends quota. `robots.txt` disallows everything, but only polite
bots honour it. Searches, item pages and short links are limited to 30 in a
row per IP (refilled at 30 per minute) and photo searches to 5, which keeps a
single client from draining the quota; for anything stricter, put the
instance behind authentication.

## Running it

```bash
cp .env.example .env && chmod 600 .env   # fill in your keyset
cp docker-compose.example.yml docker-compose.yml
docker compose up -d --build
```

The image is a `scratch` container with a single static binary; templates,
static files and the time zone database are embedded. The container needs
outbound access to `api.ebay.com`, `i.ebayimg.com`, `www.ecb.europa.eu` and
whatever hosts sellers use for their description images.

### Configuration

| Flag | Environment | Default | Meaning |
| --- | --- | --- | --- |
| | `EBAY_CLIENT_ID` | | App ID (Client ID) of your keyset |
| | `EBAY_CLIENT_SECRET` | | Cert ID (Client Secret) of your keyset |
| | `SIMPLEEBAY_ENVIO_PAIS` | | Destination country (ISO code) for shipping costs, import charges and delivery estimates |
| | `SIMPLEEBAY_ENVIO_CP` | | Destination postal code; also enables sorting by distance |
| | `TZ` | `UTC` | Time zone for auction end times, e.g. `Europe/Madrid` |
| `-h` | `SIMPLEEBAY_HOST` | `0.0.0.0` | Listen address |
| `-p` | `SIMPLEEBAY_PORT` | `8080` | Listen port |
| `-probe` | | | Print the raw JSON of an API path and exit |
| `-mp` | | `EBAY_ES` | Marketplace used by `-probe` |
| | `GOMEMLIMIT` | `32MiB` | Soft memory limit, sized for a 64 MB container |

### Behind a reverse proxy

- Pass the client address in `X-Real-IP`. It is only trusted when the
  connection comes from a private or loopback address, so it cannot be forged
  from outside; without it, every visitor shares one rate limit.
- Keep the original `Host` and set `X-Forwarded-Proto`: photo uploads are
  checked against them when the browser does not send `Sec-Fetch-Site`.
- Allow request bodies of about 6 MB on `/foto` (photos are capped at 5 MB).

### Routes

| Route | Purpose |
| --- | --- |
| `/` | Home page |
| `/s?q=…` | Search. Other parameters: `mp`, `vendedor`, `categoria`, `asp` (`Name:Value`, repeatable), `epid`, `orden`, `estado`, `compra`, `desde`, `min`, `max`, `pagina`. Needs at least one of `q`, `vendedor`, `categoria` or `epid` |
| `/itm/{id}` | Item page; `?var={id}` selects a variation |
| `/m/{code}` | Opens an `ebay.io/m/{code}` short link |
| `/foto` | Search by photo (`POST`, multipart: `foto`, `mp`) |
| `/img/…` | Proxy for photos on `i.ebayimg.com` |
| `/ext?u=…&f=…` | Proxy for signed external images in descriptions |

## Redirecting eBay links

With a browser extension such as
[Redirector](https://einaregilsson.com/redirector/), eBay links can open in
SimpleEbay automatically. Use regular expressions, apply them to the main
window only, and keep this order, since the first matching rule wins.
Replace `https://simpleebay.example.com` with your instance.

The first rule is for share links and is complete as shown. Every other
pattern starts with the same domain prefix:
`^https?://(?:[a-z]+.)?ebay.(?:co.uk|com.(?:au|hk|sg|my)|[a-z]{2,3})`

| # | Pattern (after the prefix) | Redirect to |
| --- | --- | --- |
| 1 | `^https?://ebay\.io/m/([A-Za-z0-9]+)` (no prefix) | `https://simpleebay.example.com/m/$1` |
| 2 | `/itm/(?:[^/?#]+/)?(\d+)\?(?:[^#]*&)?var=(\d+).*$` | `https://simpleebay.example.com/itm/$1?var=$2` |
| 3 | `/itm/(?:[^/?#]+/)?(\d+)(?:[/?#].*)?$` | `https://simpleebay.example.com/itm/$1` |
| 4 | `/ws/eBayISAPI\.dll\?ViewItem(?:[^#]*&)item=(\d+).*$` | `https://simpleebay.example.com/itm/$1` |
| 5 | `/rover/[^?#]*\?(?:[^#]*&)?mpre=https?%3A%2F%2F[^&#]*?%2Fitm%2F(?:[^&#]*?%2F)?(\d+).*$` | `https://simpleebay.example.com/itm/$1` |
| 6 | `/p/(?:[^/?#]+/)?(\d+)(?:[/?#].*)?$` | `https://simpleebay.example.com/s?epid=$1` |
| 7 | `/usr/([A-Za-z0-9._*-]+)(?:[/?#].*)?$` | `https://simpleebay.example.com/s?vendedor=$1` |
| 8 | `/sch/[^?#]*\?(?:[^#]*&)?_ssn=([^&#]+).*$` | `https://simpleebay.example.com/s?vendedor=$1` |
| 9 | `/sch/[^?#]*\?(?:[^#]*&)?_nkw=([^&#]+).*$` | `https://simpleebay.example.com/s?q=$1` |
| 10 | `/b/[^/?#]+/([1-9]\d*)(?:[/?#].*)?$` | `https://simpleebay.example.com/s?categoria=$1` |
| 11 | `/sch/[^?#]*\?(?:[^#]*&)?_sacat=([1-9]\d*).*$` | `https://simpleebay.example.com/s?categoria=$1` |
| 12 | `(?:[/?#].*)?$` | `https://simpleebay.example.com/` |

They cover share links, listings (with and without a variation), old
`ViewItem` URLs, affiliate links, catalogue product pages, seller profiles and
"seller's other items", searches and category pages. The last one sends
anything else on eBay to the home page, so the browser never lands on eBay.
Categories open in the marketplace selected in SimpleEbay, not the one in the
link.

## Limitations

These come from the API, not from SimpleEbay:

- Only 16 marketplaces: AT, AU, BE, CA, CH, DE, ES, FR, GB, HK, IE, IT, NL,
  PL, SG and US. Japan, Malaysia, the Philippines and India are rejected.
- Only active listings; no sold or completed ones.
- Product reviews come as an average rating only, without their text.
- Titles come already translated into the marketplace's language, and the
  original is not available.
- Sorting by price always includes shipping; there is no price-only sort.
- eBay's price filter ignores shipping, so the range is applied again on the
  server to the total with shipping.
- Any sort order other than best match only returns listings with every word
  of the search in the title.
- Search by photo only exists on the German, British, American and
  Australian marketplaces, and its results cannot be sorted or filtered
  without uploading the photo again.
- Only search results say which catalogue product a listing belongs to, so
  "compare with other sellers" is linked from the results, not from item
  pages.

## Development notes

- Probe the real API response before mapping any field:
  `simpleebay -probe '/buy/browse/v1/item_summary/search?q=game+boy&limit=1'`
- Inside the container: `docker exec simpleebay_app /simpleebay -probe '...'`
- Check the remaining daily quota:
  `docker exec simpleebay_app /simpleebay -probe '/developer/analytics/v1_beta/rate_limit/?api_context=buy'`
- Without Go installed on the host, manage dependencies with the build image:
  `docker run --rm -u "$(id -u):$(id -g)" -v "$PWD":/src -w /src -e HOME=/tmp -e GOCACHE=/tmp/cache -e GOMODCACHE=/tmp/mod golang:1.26-alpine go mod tidy`

## License

AGPL-3.0-or-later.

SimpleEbay is an independent project and is not affiliated with eBay Inc.
