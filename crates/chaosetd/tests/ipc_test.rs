#[cfg(unix)]
#[test]
fn test_ipc_round_trip() {
    use std::os::unix::net::{UnixListener, UnixStream};
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::sync::Arc;
    use std::thread;
    use std::time::Duration;

    use chaoset_engine::{Dictionary, UserDict};
    use chaosetd::ipc::{read_message, write_message, Request, Response};
    use chaosetd::server::{run_unix_server, ServerConfig};
    use serde_json::json;

    let socket_path = format!("/tmp/chaoset_test_{}.sock", std::process::id());
    let _ = std::fs::remove_file(&socket_path);

    let listener = UnixListener::bind(&socket_path).unwrap();

    let tsv_data = "你好\tni'hao\t9999\n实施\tshi'shi\t500\n试试\tshi'shi\t200\n";
    let mut dict = Dictionary::new();
    dict.load_tsv_str(tsv_data);
    let user_dict = Arc::new(UserDict::new::<&str>(None));

    let running = Arc::new(AtomicBool::new(true));
    let server_running = Arc::clone(&running);
    let config = ServerConfig {
        dict: Arc::new(dict),
        user_dict: Some(user_dict),
        fuzzy: false,
        traditional: false,
        shuangpin: None,
    };

    let server_handle = thread::spawn(move || {
        let _ = run_unix_server(listener, config, server_running);
    });

    thread::sleep(Duration::from_millis(50));

    let mut stream = UnixStream::connect(&socket_path).unwrap();

    // 1. Ping
    let req = Request {
        id: 1,
        method: "ping".to_string(),
        params: None,
    };
    write_message(&mut stream, &req).unwrap();
    let resp: Response = read_message(&mut stream).unwrap().unwrap();
    assert_eq!(resp.id, 1);
    assert_eq!(resp.result.unwrap(), json!("pong"));

    // 2. Input "nihao"
    let req = Request {
        id: 2,
        method: "input".to_string(),
        params: Some(json!({ "text": "nihao", "half": false })),
    };
    write_message(&mut stream, &req).unwrap();
    let resp: Response = read_message(&mut stream).unwrap().unwrap();
    assert_eq!(resp.id, 2);
    let res = resp.result.unwrap();
    assert_eq!(res["preedit"], "ni'hao");
    let cands = res["candidates"].as_array().unwrap();
    assert!(!cands.is_empty());
    assert_eq!(cands[0]["text"], "你好");

    // 3. Select 0
    let req = Request {
        id: 3,
        method: "select".to_string(),
        params: Some(json!({ "index": 0 })),
    };
    write_message(&mut stream, &req).unwrap();
    let resp: Response = read_message(&mut stream).unwrap().unwrap();
    assert_eq!(resp.id, 3);
    assert_eq!(resp.result.unwrap()["committed"], "你好");

    // 4. Punct
    let req = Request {
        id: 4,
        method: "punct".to_string(),
        params: Some(json!({ "key": ",", "half": false })),
    };
    write_message(&mut stream, &req).unwrap();
    let resp: Response = read_message(&mut stream).unwrap().unwrap();
    assert_eq!(resp.id, 4);
    assert_eq!(resp.result.unwrap()["committed"], "，");

    // 5. UserStats
    let req = Request {
        id: 5,
        method: "userStats".to_string(),
        params: None,
    };
    write_message(&mut stream, &req).unwrap();
    let resp: Response = read_message(&mut stream).unwrap().unwrap();
    assert_eq!(resp.id, 5);
    let stats = resp.result.unwrap();
    assert!(stats["words"].as_u64().unwrap() >= 1);

    // 6. UserExport
    let req = Request {
        id: 6,
        method: "userExport".to_string(),
        params: Some(json!({ "phrases_only": false, "offset": 0, "limit": 10 })),
    };
    write_message(&mut stream, &req).unwrap();
    let resp: Response = read_message(&mut stream).unwrap().unwrap();
    assert_eq!(resp.id, 6);
    let exp = resp.result.unwrap();
    assert!(exp["tsv"].as_str().unwrap().contains("你好"));

    // 7. UserDelete
    let req = Request {
        id: 7,
        method: "userDelete".to_string(),
        params: Some(json!({ "word": "你好" })),
    };
    write_message(&mut stream, &req).unwrap();
    let resp: Response = read_message(&mut stream).unwrap().unwrap();
    assert_eq!(resp.id, 7);
    assert_eq!(resp.result.unwrap()["deleted"], true);

    // 8. UserClear
    let req = Request {
        id: 8,
        method: "userClear".to_string(),
        params: None,
    };
    write_message(&mut stream, &req).unwrap();
    let resp: Response = read_message(&mut stream).unwrap().unwrap();
    assert_eq!(resp.id, 8);
    assert_eq!(resp.result.unwrap()["cleared"], true);

    // Clean up
    drop(stream);
    running.store(false, Ordering::SeqCst);
    let _ = server_handle.join();
    let _ = std::fs::remove_file(&socket_path);
}
