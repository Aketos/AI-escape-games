import asyncio
import websockets
import json

async def test():
    uri = "ws://67.185.96.105:50168/api/chat"
    try:
        async with websockets.connect(uri) as websocket:
            print("Connected")
            
            # Send a simple text message
            msg = {"type": "setup", "text": "Hello"}
            await websocket.send(json.dumps(msg))
            
            while True:
                response = await websocket.recv()
                print(f"< {response}")
    except Exception as e:
        print(f"Error: {e}")

asyncio.run(test())
