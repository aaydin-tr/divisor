# ip-hash places Virtual nodes by the config alone

ip-hash places a Backend's Virtual nodes (CONTEXT.md) by its Backend address and its **address occurrence**: how many entries before it in the current config share that address (`addr#0`, `addr#1`). Nothing from the process's history goes in, so every divisor running one config, freshly booted or Reloaded into it, routes each Client IP to the same Backend address. 1.0.0 placed by Index, which depends on Reload history: two divisor instances behind one Service, one Reloaded from `[A, B, C]` to `[A, C, D]` and one booted on `[A, C, D]`, sent 619 of 1000 Client IPs to different Backends. This amends ADR 0006: its "Renumber Indexes" option no longer bears on ip-hash (the `backend_hash` reason stands alone), and its consequence that ip-hash keeps a reference to every Backend it ever Joined no longer holds.

The Pool numbers address occurrences in config order at boot and on every Reload. A survivor whose occurrence a Reload shifts (an entry at its address added or removed before it, or two entries at one address swapped) is re-placed through `pool.RingBalancer`, new positions before old, so it never leaves the ring; Balancers that do not place by address are not told. The ring (`pkg/consistent`) records which Backend holds each position under which key. A Backend Joining under a key takes over that key's positions, and a Leave removes only the positions its Backend still holds, so a changed entry (ADR 0006: a removal plus an addition, the addition Joining first) keeps the clients of the entry it replaced. Two keys hashing to one position resolve to the lower key whatever the Join order. A Pick resolves its Backend from the ring snapshot it read, so no map from Index to Backend outlives a removed Backend.

## Considered Options

- **Place by Index and renumber Indexes to config positions** — rejected for ADR 0006's reason: `backend_hash` must keep pointing at one Backend across Reloads.
- **Place by address alone** — rejected: two entries at one address (the way to give a server a double share) would land on the same positions and collide, as 0.x did.
- **Place by the whole entry (address and Probe settings)** — rejected: changing a Backend's `health_check_path` would move its clients.
- **Leave a survivor whose occurrence shifted where it is** — rejected: two differing entries at one address, then a Reload adding, removing or reordering one, would leave divisor instances disagreeing until a restart.
- **Re-place with Leave then Join on every Balancer** — rejected: the only Alive Backend would be out of the rotation in between (a 503), and round-robin and w-round-robin would reorder for a placement only ip-hash has.

## Consequences

- Upgrading from 1.0.0 moves most ip-hash Client IPs to a different Backend once, as the 0.x to 1.0 upgrade did.
- Every `pool.Backend` carries an `AddressOccurrence`; only a `pool.RingBalancer` reads it, and only the Pool's single writer changes it.
- An entry whose `url` changes is a new address: its clients move, as with any added Backend.
