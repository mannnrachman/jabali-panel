# Platform — Mail Autoconfig

Bulwark serves three autoconfig flavours so clients pick up correct mail settings automatically.

## Endpoints

| Client family | URL | Format |
|---|---|---|
| **Thunderbird, K-9, etc.** | `https://autoconfig.<domain>/mail/config-v1.1.xml?emailaddress=<address>` | Mozilla autoconfig XML |
| **Outlook** | `https://autodiscover.<domain>/autodiscover/autodiscover.xml` (POST) | Microsoft Autodiscover XML |
| **Apple Mail, iOS** | `https://<panel-hostname>/.well-known/mobileconfig?email=<address>` | Apple `.mobileconfig` signed profile |

(For Outlook, also handle the SRV-record fallback: `_autodiscover._tcp.<domain>` SRV → panel hostname. The installer suggests adding it; the panel does not push the SRV automatically.)

## Settings advertised

All three return:

| Field | Value |
|---|---|
| SMTP submission | `smtp.<panel-hostname>` `:587` STARTTLS or `:465` TLS |
| IMAP | `imap.<panel-hostname>` `:993` TLS |
| POP3 (only if Stalwart enables it) | `pop3.<panel-hostname>` `:995` TLS |
| Username | full email address |
| Auth | plain (over TLS) |

## DNS prerequisites

For autoconfig.x.com and autodiscover.x.com to work, the operator publishes:

```
autoconfig.example.com.   CNAME <panel-hostname>.
autodiscover.example.com. CNAME <panel-hostname>.
```

The panel doesn't manage these automatically (the domain may not have its DNS hosted on Jabali). They're displayed as recommended records under Domains → Edit → DNS.

## The mobileconfig flow

Apple's signed `.mobileconfig` profiles need to be signed by a cert chain the client trusts. The panel signs with its **panel-hostname cert** (Let's Encrypt, see [ssl.md](../ssl.md#panel-hostname-certificate)). If the panel cert is self-signed (bootstrap state, before LE issued), the mobileconfig won't validate on iOS — first-boot warning.

## Why Bulwark, not the panel API

Bulwark already runs the SPA + login bridge, and the autoconfig endpoints are tied to the same routing primitives (host header → recognised domain → respond). Putting them in the panel API would have meant another route family + extra TLS termination. Bulwark unifies the surface.
