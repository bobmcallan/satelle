#!/usr/bin/env python3
# A stand-in for `pi --mode rpc` used by the agentcli and agentstep tests. It
# records its argv and every record satelle writes to it in FAKE_PI_RECORD, and
# plays the run FAKE_PI_MODE names in pi's documented record shapes:
#
#   answer  two turns with one grep tool call, then agent_settled; it answers
#           get_last_assistant_text (FAKE_PI_ANSWER) and get_session_stats until
#           stdin closes. With FAKE_PI_LINGER set it starts a grandchild and
#           ignores stdin EOF.
#   dialog  as answer, but blocks on a confirm dialog until satelle answers it.
#   turns   starts a turn every 50ms until satelle sends abort.
#   stall   starts one turn, then spins on CPU and emits nothing more.
import json, os, select, subprocess, sys, time

mode = os.environ.get("FAKE_PI_MODE", "answer")
rec_path = os.environ.get("FAKE_PI_RECORD")
answer = os.environ.get("FAKE_PI_ANSWER", "the answer")

def record(obj):
    if rec_path:
        with open(rec_path, "a") as f:
            f.write(json.dumps(obj) + "\n")

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

def read():
    line = sys.stdin.readline()
    if not line:
        return None
    obj = json.loads(line)
    record({"in": obj})
    return obj

record({"pid": os.getpid(), "argv": sys.argv[1:]})
stats = {"tokens": {"input": 9926, "output": 450, "cacheRead": 10446, "cacheWrite": 74, "total": 20896}, "cost": 0.399, "toolCalls": 1}

def serve():
    while True:
        cmd = read()
        if cmd is None:
            return
        t = cmd.get("type")
        if t == "get_last_assistant_text":
            send({"id": cmd.get("id"), "type": "response", "command": t, "success": True, "data": {"text": answer}})
        elif t == "get_session_stats":
            send({"id": cmd.get("id"), "type": "response", "command": t, "success": True, "data": stats})

prompt = read()
send({"id": prompt.get("id"), "type": "response", "command": "prompt", "success": True, "data": {"disposition": "started"}})
send({"type": "agent_start"})

if mode == "turns":
    n = 0
    while True:
        n += 1
        send({"type": "turn_start"})
        ready, _, _ = select.select([sys.stdin], [], [], 0.05)
        if ready:
            cmd = read()
            if cmd and cmd.get("type") == "abort":
                record({"abort_after_turn": n})
                send({"id": cmd.get("id"), "type": "response", "command": "abort", "success": True})
                send({"type": "agent_end", "messages": [], "willRetry": False})
                send({"type": "agent_settled", "aborted": True})
                break
    serve()
    sys.exit(0)

if mode == "stall":
    send({"type": "turn_start"})
    while True:
        pass

send({"type": "turn_start"})
send({"type": "message_update", "assistantMessageEvent": {"type": "text_delta", "contentIndex": 0, "delta": "looking"}})
send({"type": "tool_execution_start", "toolCallId": "c1", "toolName": "grep", "args": {"pattern": "x"}})
send({"type": "tool_execution_end", "toolCallId": "c1", "toolName": "grep", "result": {}, "isError": False})
if mode == "dialog":
    send({"type": "extension_ui_request", "id": "ui-1", "method": "notify", "message": "hi"})
    send({"type": "extension_ui_request", "id": "ui-2", "method": "confirm", "title": "Allow?", "message": "m"})
    reply = read()
    if not (reply and reply.get("type") == "extension_ui_response" and reply.get("id") == "ui-2" and reply.get("cancelled") is True):
        sys.exit(3)
send({"type": "turn_start"})
msg = {"role": "assistant", "content": [{"type": "text", "text": answer}]}
if os.environ.get("FAKE_PI_MODEL"):
    msg["provider"] = os.environ.get("FAKE_PI_PROVIDER", "")
    msg["model"] = os.environ["FAKE_PI_MODEL"]
send({"type": "message_end", "message": msg})
send({"type": "agent_end", "messages": [msg], "willRetry": False})
send({"type": "agent_settled", "aborted": False})
if os.environ.get("FAKE_PI_LINGER"):
    subprocess.Popen(["sleep", "300"])
    serve()
    while True:
        time.sleep(1)
serve()
