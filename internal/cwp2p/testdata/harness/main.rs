use commonware_codec::{DecodeExt, Encode};
use commonware_cryptography::{
    ed25519, handshake::sake, transcript::Transcript, ChaCha20Poly1305, Cipher, Signer,
};
use commonware_p2p::{
    authenticated::lookup, Address, AddressableManager, Receiver as _, Recipients, Sender as _,
};
use commonware_runtime::{tokio as rt, Clock, IoBuf, Quota, Runner, Spawner, Supervisor};
use commonware_stream::{
    cups::{self, Cups},
    sake::{self as ssake, Sake},
};
use commonware_utils::{ordered::Map, union_unique, NZUsize};
use commonware_math::algebra::Random;
use rand_core::{TryCryptoRng, TryRng};
use std::{convert::Infallible, net::SocketAddr, num::NonZeroU32, time::Duration};

struct Counter(u8);

impl TryRng for Counter {
    type Error = Infallible;
    fn try_next_u32(&mut self) -> Result<u32, Infallible> {
        let mut b = [0u8; 4];
        self.try_fill_bytes(&mut b)?;
        Ok(u32::from_le_bytes(b))
    }
    fn try_next_u64(&mut self) -> Result<u64, Infallible> {
        let mut b = [0u8; 8];
        self.try_fill_bytes(&mut b)?;
        Ok(u64::from_le_bytes(b))
    }
    fn try_fill_bytes(&mut self, dst: &mut [u8]) -> Result<(), Infallible> {
        for x in dst.iter_mut() {
            *x = self.0;
            self.0 = self.0.wrapping_add(1);
        }
        Ok(())
    }
}
impl TryCryptoRng for Counter {}

fn key(seed: u8) -> ed25519::PrivateKey {
    ed25519::PrivateKey::decode(IoBuf::from(vec![seed; 32])).unwrap()
}

fn seal(c: ChaCha20Poly1305, msg: &[u8]) -> (ChaCha20Poly1305, Vec<u8>) {
    let mut data = msg.to_vec();
    let (c, tag) = c.seal(&[], &mut data).unwrap();
    data.extend_from_slice(tag.as_ref());
    (c, data)
}

fn ciphers(mut t: Transcript, v1: bool) -> (ChaCha20Poly1305, ChaCha20Poly1305) {
    if v1 {
        t = t.fork(b"_COMMONWARE_STREAM_CUPS");
        t.commit([1u8].as_slice());
    }
    (
        ChaCha20Poly1305::random(t.noise(b"cipher_l2d")),
        ChaCha20Poly1305::random(t.noise(b"cipher_d2l")),
    )
}

fn vectors() {
    let ns = union_unique(b"TEMPO", b"_P2P");
    let now: u64 = 1_700_000_000_000;
    let mut out = Vec::new();
    for (name, version) in [("v0", sake::Version::V0), ("v1", sake::Version::V1)] {
        let (dk, lk) = (key(1), key(2));
        let ctx_d = sake::Context::new(&ns, now, now - 10_000..now + 5_000, dk.clone(), lk.public_key(), version);
        let (ds, syn) = sake::dial_start(Counter(10), ctx_d);
        let ctx_l = sake::Context::new(&ns, now, now - 10_000..now + 5_000, lk.clone(), dk.public_key(), version);
        let syn_hex = hex::encode(syn.encode());
        let (ls, synack) = sake::listen_start(Counter(100), ctx_l, syn).unwrap();
        let synack_hex = hex::encode(synack.encode());
        let (ack, td) = sake::dial_end(ds, synack).unwrap();
        let ack_hex = hex::encode(ack.encode());
        let tl = sake::listen_end(ls, ack).unwrap();
        assert_eq!(td.summarize(), tl.summarize());
        let summary_hex = hex::encode(td.summarize().encode());
        let v1 = name == "v1";
        let (l2d, d2l) = ciphers(td, v1);
        let (d2l, rec1) = seal(d2l, b"hello");
        let (_, rec2) = seal(d2l, b"");
        let (_, rec3) = seal(l2d, b"back");
        out.push(serde_json::json!({
            "name": name,
            "namespace": hex::encode(&ns),
            "now_ms": now,
            "dialer_seed": hex::encode([1u8; 32]),
            "listener_seed": hex::encode([2u8; 32]),
            "dialer_entropy_start": 10,
            "listener_entropy_start": 100,
            "dialer_public": hex::encode(dk.public_key().encode()),
            "listener_public": hex::encode(lk.public_key().encode()),
            "syn": syn_hex,
            "synack": synack_hex,
            "ack": ack_hex,
            "summary": summary_hex,
            "d2l_seal_hello": hex::encode(rec1),
            "d2l_seal_empty": hex::encode(rec2),
            "l2d_seal_back": hex::encode(rec3),
        }));
    }
    println!("{}", serde_json::to_string_pretty(&out).unwrap());
}

const CHANNELS: [(u64, u32); 6] = [(0, 128), (1, 128), (2, 128), (3, 8), (4, 8), (5, 128)];

fn arg(args: &[String], name: &str) -> Option<String> {
    args.iter().position(|a| a == name).map(|i| args[i + 1].clone())
}

fn node(args: Vec<String>) {
    let seed = u8::from_str_radix(&arg(&args, "--seed").unwrap(), 10).unwrap();
    let listen: SocketAddr = arg(&args, "--listen").unwrap().parse().unwrap();
    let secs: u64 = arg(&args, "--secs").unwrap_or("30".into()).parse().unwrap();
    let count: u64 = arg(&args, "--count").unwrap_or("5".into()).parse().unwrap();
    let peers: Vec<(ed25519::PublicKey, SocketAddr)> = args
        .iter()
        .enumerate()
        .filter(|(_, a)| *a == "--peer")
        .map(|(i, _)| {
            let (k, a) = args[i + 1].split_once('@').unwrap();
            let pk = ed25519::PublicKey::decode(IoBuf::from(hex::decode(k).unwrap())).unwrap();
            (pk, a.parse().unwrap())
        })
        .collect();
    let signer = key(seed);
    println!("PUBKEY {}", hex::encode(signer.public_key().encode()));
    let ns = union_unique(b"TEMPO", b"_P2P");
    let mut cfg = lookup::Config::local(
        Cups::<_, ChaCha20Poly1305>::new(
            Sake {
                signer: signer.clone(),
                synchrony_bound: Duration::from_secs(5),
                max_handshake_age: Duration::from_secs(10),
                version: ssake::Version::V0,
            },
            cups::Version::V0,
        ),
        &ns,
        listen,
        NZUsize!(128),
        8_388_780,
        NZUsize!(1024),
    );
    cfg.tracked_peer_sets = NZUsize!(1);
    cfg.dial_frequency = Duration::from_secs(1);
    cfg.ping_frequency = Duration::from_secs(50);
    let runner = rt::Runner::new(rt::Config::default());
    runner.start(|context| async move {
        let (mut network, mut oracle) = lookup::Network::new(context.child("network"), cfg);
        let mut set: Vec<(ed25519::PublicKey, Address)> = vec![(signer.public_key(), listen.into())];
        for (pk, a) in &peers {
            set.push((pk.clone(), (*a).into()));
        }
        let map: Map<_, Address> = set.try_into().unwrap();
        let _ = oracle.track(0, map);
        let mut senders = Vec::new();
        for (ch, rate) in CHANNELS {
            let (s, mut r) = network.register(ch, Quota::per_second(NonZeroU32::new(rate).unwrap()));
            senders.push((ch, s));
            context.child("recv").spawn(move |_| async move {
                let mut got = 0u64;
                loop {
                    match r.recv().await {
                        Ok((from, msg)) => {
                            got += 1;
                            let data = msg.as_ref().to_vec();
                            println!("RECV ch={} from={} len={} n={} data={}", ch, hex::encode(from.encode()), data.len(), got, hex::encode(&data[..data.len().min(64)]));
                        }
                        Err(e) => {
                            println!("RECVERR ch={} {:?}", ch, e);
                            return;
                        }
                    }
                }
            });
        }
        network.start();
        let start = context.current();
        let mut n = 0u64;
        while context.current().duration_since(start).unwrap() < Duration::from_secs(secs) {
            context.sleep(Duration::from_millis(500)).await;
            if n >= count {
                continue;
            }
            let mut any = false;
            for (ch, s) in senders.iter_mut() {
                let mut payload = format!("rust ch={} n={} ", ch, n).into_bytes();
                if *ch == 4 && n == 0 {
                    payload.resize(200_000, 0xab);
                }
                let sent = s.send(Recipients::All, IoBuf::from(payload), false);
                if !sent.is_empty() {
                    any = true;
                    println!("SENT ch={} n={} to={}", ch, n, sent.iter().map(|p| hex::encode(p.encode())).collect::<Vec<_>>().join(","));
                }
            }
            if any {
                n += 1;
            }
        }
        println!("DONE sent_rounds={}", n);
    });
}

fn main() {
    tracing_subscriber::fmt().with_env_filter(tracing_subscriber::EnvFilter::from_default_env()).with_writer(std::io::stderr).init();
    let args: Vec<String> = std::env::args().collect();
    match args.get(1).map(|s| s.as_str()) {
        Some("vectors") => vectors(),
        Some("node") => node(args),
        _ => eprintln!("usage: cwp2p-harness vectors | node --seed N --listen ADDR [--peer HEX@ADDR] [--secs S] [--count K]"),
    }
}
