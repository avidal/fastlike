// The spec guest keeps using deprecated SDK APIs on purpose, since the
// hostcalls behind them still need coverage.
#![allow(deprecated)]

use fastly::{Request, Response, Body, Error};
use fastly::http::{Method, StatusCode};
use fastly::experimental::uap_parse;
use fastly::geo::geo_lookup;
use serde_json;

const BACKEND: &str = "backend";

#[fastly::main]
fn main(mut req: Request) -> Result<Response, Error> {
    match (req.get_method(), req.get_url().path()) {
        (&Method::GET, "/simple-response") => Ok(Response::new()
            .with_status(200)
            .with_body("Hello, world!")
        ),

        (&Method::GET, "/no-body") => Ok(Response::new()
            .with_status(StatusCode::NO_CONTENT)
        ),

        (&Method::GET, "/user-agent") => {
            let ua = req.get_header("user-agent");
            let result = match ua {
                Some(inner) => {
                    uap_parse(inner.to_str()?)
                },
                None => uap_parse(""),
            };
            let s = match result {
                Ok((family, major, minor, patch)) => {
                    format!("{} {}.{}.{}",
                            family,
                            major.unwrap_or("0".to_string()),
                            minor.unwrap_or("0".to_string()),
                            patch.unwrap_or("0".to_string())
                    )
                },
                Err(_) => { "error".to_string() },
            };
            Ok(Response::new()
               .with_status(200)
               .with_body(s)
            )
        },

        (&Method::GET, "/append-header") => {
            req.set_header("test-header", "test-value");
            Ok(req.send(BACKEND)?)
        },

        (&Method::GET, "/append-body") => {
            let mut rv = Response::from_body("original\n");
            rv.append_body(Body::from("appended"));
            Ok(rv)
        },

        (&Method::GET, path) if path.starts_with("/proxy") => {
            Ok(req.send(BACKEND)?)
        },

        (&Method::GET, "/panic!") => {
            panic!("you told me to");
        },

        (&Method::GET, "/geo") => {
            let ip = req.get_client_ip_addr();
            if ip.is_none() {
                return Ok(Response::from_status(500));
            }
            let geodata = geo_lookup(ip.unwrap()).unwrap();
            Ok(Response::new()
                .with_status(200)
                .with_body(
                    serde_json::json!({
                        "as_name": geodata.as_name(),
                    }).to_string()
                )
            )
        },

        (&Method::GET, "/log") => {
            use std::io::Write;
            use fastly::log::Endpoint;
            let mut endpoint = Endpoint::from_name("default");
            writeln!(endpoint, "Hello from fastlike!").unwrap();
            Ok(Response::from_status(StatusCode::NO_CONTENT))
        },

        (&Method::GET, path) if path.starts_with("/dictionary") => {
            // open the dictionary and get the key specified in the path
            let parts: Vec<&str> = path[1..].split("/").collect();
            let (name, key) = (parts[1], parts[2]);
            use fastly::Dictionary;
            let dict = Dictionary::open(name);
            let value = dict.get(key).unwrap();
            Ok(Response::new().with_status(200).with_body(value))
        },

        (&Method::GET, "/core-cache") => Ok(Response::from_body(core_cache_states()?)),

        // Fills the core cache with a backend body, and returns before the
        // backend is done sending it.
        (&Method::GET, "/cache-fill") => {
            use fastly::cache::core::{insert, CacheKey};
            let resp = Request::get("http://origin/fill").with_pass(true).send(BACKEND)?;
            let mut body = insert(CacheKey::from_static(b"filled"), std::time::Duration::from_secs(60)).execute()?;
            body.append(resp.into_body());
            body.finish()?;
            Ok(Response::from_status(StatusCode::NO_CONTENT))
        },

        (&Method::GET, "/cache-read") => {
            use fastly::cache::core::{lookup, CacheKey};
            match lookup(CacheKey::from_static(b"filled")).execute()? {
                Some(found) => Ok(Response::from_body(found.to_stream()?)),
                None => Ok(Response::from_status(StatusCode::NOT_FOUND)),
            }
        },

        _ => Ok(Response::new()
            .with_status(404)
            .with_body("The page you requested could not be found")
        ),
    }
}

// Reports what the core cache API sees for objects of various ages.
fn core_cache_states() -> Result<String, Error> {
    use fastly::cache::core::{insert, lookup, CacheKey, Transaction};
    use std::io::Write;
    use std::time::Duration;

    let minute = Duration::from_secs(60);
    let store = |key: &'static str, initial_age: Duration, swr: Duration| -> Result<(), Error> {
        let mut body = insert(CacheKey::from_static(key.as_bytes()), minute)
            .initial_age(initial_age)
            .stale_while_revalidate(swr)
            .execute()?;
        body.write_all(key.as_bytes())?;
        body.finish()?;
        Ok(())
    };
    let describe = |tx: &Transaction| {
        format!(
            "found={} stale={} must_insert={} must_insert_or_update={}",
            tx.found().is_some(),
            tx.found().is_some_and(|found| found.is_stale()),
            tx.must_insert(),
            tx.must_insert_or_update()
        )
    };
    let mut out = Vec::new();

    store("fresh", Duration::ZERO, Duration::ZERO)?;
    let found = lookup(CacheKey::from_static(b"fresh")).execute()?;
    out.push(format!(
        "fresh: found={} stale_while_revalidate={:?}",
        found.is_some(),
        found.map(|found| found.stale_while_revalidate())
    ));

    // Handles keep their lookup's hit count, and complete objects report a
    // length.
    let mut body = insert(CacheKey::from_static(b"unsized"), minute).execute()?;
    body.write_all(b"0123456789")?;
    body.finish()?;
    let first = lookup(CacheKey::from_static(b"unsized")).execute()?;
    let second = lookup(CacheKey::from_static(b"unsized")).execute()?;
    out.push(format!(
        "snapshot: first_hits={:?} second_hits={:?} length={:?}",
        first.as_ref().map(|found| found.hits()),
        second.as_ref().map(|found| found.hits()),
        first.as_ref().map(|found| found.known_length())
    ));

    store("expired", 2 * minute, Duration::ZERO)?;
    let found = lookup(CacheKey::from_static(b"expired")).execute()?;
    out.push(format!("expired lookup: found={}", found.is_some()));
    let tx = Transaction::lookup(CacheKey::from_static(b"expired")).execute()?;
    out.push(format!("expired transaction: {}", describe(&tx)));
    tx.cancel_insert_or_update()?;

    store("stale", Duration::from_secs(90), minute)?;
    let leader = Transaction::lookup(CacheKey::from_static(b"stale")).execute()?;
    out.push(format!("stale transaction: {}", describe(&leader)));
    let follower = Transaction::lookup(CacheKey::from_static(b"stale")).execute()?;
    out.push(format!("stale second transaction: {}", describe(&follower)));
    leader.cancel_insert_or_update()?;

    Ok(out.join("\n"))
}
