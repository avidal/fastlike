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

        _ => Ok(Response::new()
            .with_status(404)
            .with_body("The page you requested could not be found")
        ),
    }
}
