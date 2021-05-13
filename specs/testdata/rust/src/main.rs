use fastly::http::{HeaderValue, Method, StatusCode};
use fastly::request::downstream_client_ip_addr;
use fastly::geo::geo_lookup;
use fastly::{Body, Error, Request, RequestExt, Response, ResponseExt};
use fastly::uap_parse;
use serde_json;

const BACKEND: &str = "backend";

#[fastly::main]
fn main(mut req: Request<Body>) -> Result<impl ResponseExt, Error> {
    match (req.method(), req.uri().path()) {
        (&Method::GET, "/simple-response") => Ok(Response::builder()
            .status(StatusCode::OK)
            .body(Body::from("Hello, world!"))?
        ),

        (&Method::GET, "/no-body") => Ok(Response::builder()
            .status(StatusCode::NO_CONTENT)
            .body(Body::new())?),

        (&Method::GET, "/user-agent") => {
            let ua = req.headers().get("user-agent");
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
            Ok(Response::builder()
               .status(StatusCode::OK)
               .body(Body::from(s))?)
        },

        (&Method::GET, "/append-header") => {
            req.headers_mut().insert("test-header", HeaderValue::from_static("test-value"));
            Ok(req.send(BACKEND)?)
        },

        (&Method::GET, "/append-body") => {
            let other = Body::from("appended");
            let rw = Response::new(Body::from("original\n"));
            let (mut parts, mut body) = rw.into_parts();
            body.append(other);
            parts.status = StatusCode::OK;
            let rv = Response::from_parts(parts, body);
            Ok(rv)
        },

        (&Method::GET, path) if path.starts_with("/proxy") => {
            Ok(req.send(BACKEND)?)
        },

        (&Method::GET, "/panic!") => {
            panic!("you told me to");
        },

        (&Method::GET, "/geo") => {
            let ip = downstream_client_ip_addr();
            if ip.is_none() {
                return Ok(Response::builder().status(StatusCode::INTERNAL_SERVER_ERROR).body(Body::new())?);
            }
            let geodata = geo_lookup(ip.unwrap()).unwrap();
            Ok(Response::builder()
                .status(StatusCode::OK)
                .body(
                    Body::from(
                        serde_json::json!({
                            "as_name": geodata.as_name(),
                        }).to_string()
                    )
                )?
            )
        },

        (&Method::GET, "/log") => {
            use std::io::Write;
            use fastly::log::Endpoint;
            let mut endpoint = Endpoint::from_name("default");
            writeln!(endpoint, "Hello from fastlike!").unwrap();
            Ok(Response::builder().status(StatusCode::NO_CONTENT).body(Body::new())?)
        },

        (&Method::GET, path) if path.starts_with("/dictionary") => {
            // open the dictionary and get the key specified in the path
            let parts: Vec<&str> = path[1..].split("/").collect();
            let (name, key) = (parts[1], parts[2]);
            use fastly::dictionary::Dictionary;
            let dict = Dictionary::open(name);
            let value = dict.get(key).unwrap();
            Ok(Response::builder().status(StatusCode::OK).body(Body::from(value))?)
        },

        _ => Ok(Response::builder()
            .status(StatusCode::NOT_FOUND)
            .body(Body::from("The page you requested could not be found"))?
        ),
    }
}
