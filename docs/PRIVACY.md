# Privacy Policy

This policy explains what the Finalechat service at
<https://www.finalechat.com> (the "Service") stores about you, why, and what
you can do about it. It is written to be read, not skimmed; it is short
because the Service collects little. Eric Florenzano operates the Service and
is the data controller. If you run your own copy of the Finalechat software,
this policy does not apply to that copy.

## What we store

**Account.** Your email address, a hash of your password (argon2id; we never
store the password itself), an optional display name, your settings, and the
time you signed up.

**Sessions and tokens.** Browser sessions (a random token, the browser's user
agent string and timestamps) and API tokens for your agents. Tokens are
stored hashed; the secret is shown once and cannot be recovered.

**Your threads.** Everything posted to your account: messages, questions and
their answers, status lines, thread titles and metadata, and the files
attached to messages. Agents you connect post most of this, so it can contain
whatever those agents send, such as terminal output, screenshots or source
code. Session archives, when an integration publishes them, contain the
integration's own logs, which can include the commands it ran and their
output.

**Devices.** The push subscription each device registers: the push endpoint
issued by the browser vendor, an encryption key for that subscription, and
the device's user agent string.

**Connectors.** For integrations that expose settings controls, a hashed
credential per connector and the settings snapshots and commands exchanged
with it.

**Server logs.** Request lines with the client IP address, response status
and timing, kept for a short period to operate and secure the Service.

We do not use analytics or advertising services, and there is no tracking
beyond the single session cookie that keeps you signed in.

## Why we store it

To provide the Service you asked for: to store your threads, show them to you
and your agents, notify you, and keep your account secure. The legal basis is
the contract formed by the [Terms of Service](/terms) and, for security
logging and abuse prevention, our legitimate interest in running a safe
service. We do not sell personal data, use it for advertising, or use your
content to train models.

## Where it lives

The Service runs on servers in the United States. The database and files are
stored there and in Backblaze B2 (United States). Push notifications are
delivered through the push service of your browser vendor (for example Apple,
Google or Mozilla); the notification payload is encrypted to your device, so
those services see that a notification was sent, not what it says. If you are
outside the United States your data is transferred there to provide the
Service.

## How long we keep it

Your data stays as long as your account exists. Deleting a thread or a
message removes it and its files promptly. Deleting your account (Settings →
Account → Delete account) removes everything described above. Database
backups are made daily and kept for a limited time, currently up to one month
for daily backups and up to a year for monthly ones, after which deleted data
is gone from backups as well. Server logs are kept for at most 30 days. We
may retain specific records longer when needed to investigate abuse or a
security incident, to enforce the [Terms of Service](/terms), to establish or
defend legal claims, or when the law requires it.

## Who can see it

Only you, through the app, and the agents you connect, through the tokens you
give them. Operators of the Service can access data only to run it, to
investigate abuse or a security incident, to enforce the Terms of Service, or
when legally required, and do not browse user content. We disclose data to
third parties only to the service providers named above so they can process
it for us, when the law requires it, to protect the rights, safety or
property of users, the public or ourselves, or as part of a transfer of the
Service to a successor operator, who will be bound by this policy.

## Your rights

You can see and export your data through the app and the API, correct your
name and password in Settings, and delete your account yourself. Depending on
where you live you may also have rights to access, portability, restriction
and objection; to exercise any of them, or to complain, email
<hello@finalechat.com>. You may also complain to your local data protection
authority.

## Children

The Service is for adults. You must be at least 18 to create an account, and
we do not knowingly collect data from anyone under 18. If you believe a
minor has created an account, contact us and we will remove it.

## Changes

We may update this policy. Material changes are announced in the app or by
email before they take effect.

## Contact

<hello@finalechat.com>. The software behind the Service is open source at
<https://github.com/ericflo/finalechat>, so how data is handled can be
verified rather than taken on trust.

_Last updated: September 8, 2026._
