Mulery
======

The idea behind what's being built here is a server that can handle thousands of simultaneous client connections.
The server is essentially a command and control center for the clients. A simple server is provided, as a library
and a binary. The server is only useful with the client library that is not provided as a binary. You need to instrument
your own logic. The primary use case is to proxy http requests back into the http server running on the client.

You run the server. Then you run 5000 clients that make a persistent connect to the server and register themselves with 
a configurable id. You can now send http requests to the server with a special header that contains the client's registered
ID. The server proxies the web request back into the client through the previously-established persistent connection.

We use this so our clients do not have to open a port (port forward) for our server to communicate with the software they 
deployed on premesis. It allows our servers to distribute load and reliably reach back into the running end-user application.

How
---

Websockets and some reverse-proxy engineering.


