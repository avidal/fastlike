use fastly::http::{HeaderValue, Method, StatusCode};
use fastly::request::CacheOverride;
use fastly::{Body, Error, Request, RequestExt, Response, ResponseExt};
use fastly::downstream_request;
use std::convert::TryFrom;

/// The name of a backend server associated with this service.
///
/// This should be changed to match the name of your own backend. See the the `Hosts` section of
/// the Fastly WASM service UI for more information.
const BACKEND_NAME: &str = "backend_name";

/// The name of a second backend associated with this service.
const OTHER_BACKEND_NAME: &str = "other_backend_name";

/// The entrypoint for your application.
///
/// This function is triggered when your service receives a client request. It could be used to
/// route based on the request properties (such as method or path), send the request to a backend,
/// make completely new requests, and/or generate synthetic responses.
///
/// If `main` returns an error a 500 error response will be delivered to the client.
#[fastly::main]
fn main(mut req: Request<Body>) -> Result<impl ResponseExt, Error> {
    match (req.method(), req.uri().path()) {
        (&Method::GET, "/simple-response") => Ok(Response::builder()
            .status(StatusCode::OK)
            .body(Body::try_from("Hello, world!")?)?
        ),

        (&Method::GET, "/no-body") => Ok(Response::builder()
            .status(StatusCode::NO_CONTENT)
            .body(Body::new()?)?),

        (&Method::GET, "/append-header") => {
            req.headers_mut().insert("test-header", HeaderValue::from_static("test-value"));
            req.send(BACKEND_NAME)
        },

        (&Method::GET, path) if path.starts_with("/proxy") => {
            req.send(BACKEND_NAME)
        },

        // This one is used for example purposes, not tests
        (&Method::GET, path) if path.starts_with("/testdata") => {
            req.send(BACKEND_NAME)
        },

        _ => Ok(Response::builder()
            .status(StatusCode::NOT_FOUND)
            .body(Body::try_from("The page you requested could not be found")?)?
        ),
    }
}
