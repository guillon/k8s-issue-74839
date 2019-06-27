# Reproduction Docker issue #1090 and k8s issue #74839

This code is a fork of the original test case for k8s issue #74839
authored at https://github.com/anfernee/k8s-issue-74839

The changes are:
- describe how to reproduce docker libnetwork issue #1090
- make the SEQ overflow, instead of underflow and account for SEQ 32 bits wrap around
- add a more robust tracking of connection established and rst/fin

TODOs:
- produce issue with nodes in a docker swarm and with docker services
- produce issue over kubernetes

## Howto reproduce Docker issue #1090

Requires two different hosts/nodes such that NATed packet are routed out-of the client host.

`Client Host IP C.C.C.C <-> router <-> Server Host IP S.S.S.S`

When running on the same host, the problem does not appear anymore as packets are treated locally
and the local routing discards invalid packets before they are received by the server.

Also, in order to produce the issue, the server must not be NAT'ed such that invalid packets
getting out of the server container are not discarded before getting out of the server host.

Then the actual issue is client side, while the client container is NAT'ed (using the docker
bridge default network for instance).

The setup is thus as follow:

`Client Container IP N.N.N.N <- DNAT / SNAT -> Client Host IP C.C.C.C <-> router <-> Server HOST IP S.S.S.S:9000 <-> Server Container S.S.S.S:9000`

The sequence leading to the failure is as follow:

- Client Container Connect N.N.N.N:nnnn to Server C.C.C.C:9000
- After SNAT: Client Host Connect C.C.C.C:nnnn to Server C.C.C.C:9000
- Server receives packet: C.C.C.C:nnnn ->  S.S.S.S:9000 SYN SEQ:X
- Server responds, Client host receives packet: S.S.S.S:9000 -> C.C.C.C:nnnn SYN SEQ:Y ACK ACK:X+1
- After DNAT Client container receives and responds through SNAT, Server Receives packet: C.C.C.C:nnnn -> S.S.S.S:9000 SEQ:X+1 ACK ACK:Y+1
- Server packets are sent much more quickly than client process them (simluated here)...
- Forged Server out-of-window (+100000), Client host receives: S.S.S.S:9000 -> C.C.C.C:nnnn SYN SEQ:Y+1+100000 PSH ACK ACK:X+1 
- In iptables (somewhere before performing the DNAT) the packet is detected out-of-window and marked INVALID
- As INVALID, DNAT is aborted and the packet is presented to the host interface
- The host interface does not know any open connection to S.S.S.S:9000 and thus answers with a RST
- The actual client container did not see any packet arriving (no error on this side, except possibly a timeout)
- Hence Client host sends a RST, and Server receives: C.C.C.C:nnnn ->  S.S.S.S:9000  SEQ:X+1 ACK:0 RST
- From the server point of view, the client did a connection reset (abnormal termination)
- From the client, nothing happened, eventually the client will endup with a timeout as the server will not respond anymore.

In the test case, the failure detection is done if the server receives for the initiated conneciton (after the SYN/SYN-ACK/ACK)
a RST from the client.

In order to produce the issue, then, from the server launch:

    S.S.S.S$ make docker-issue-1090-server
    sudo ./k8s-issue-74839 9000
    Local Address: S.S.S.S:....
    listen on 0.0.0.0:9000
    probing S.S.S.S
    IP Addr: S.S.S.S
    ...

Then get to the client host and do:

    C.C.C.C$ make SERVER_HOST=S.S.S.S docker-issue-1090-client
    # Client inside a NATed container
    docker run -it alpine sh -c 'set -x; while true; do nc -w 3 S.S.S.S 9000; sleep 1; done'
    + true
    + nc -w 3 S.S.S.S 9000

If the bug is present, on the server side, the server program should exit with a failure and one should see something like:

    S.S.S.S$ ...
    conn S.S.S.S:9000-C.C.C.C:nnnn: tcp packet: &{SrcPort:nnnn DestPort:9000 Seq:1415566392 Ack:0 Flags:40962 WindowSize:29200 Checksum:50244 UrgentPtr:0}, flag: SYN , data: [], addr: C.C.C.C
    conn S.S.S.S:9000-C.C.C.C:nnnn: tcp packet: &{SrcPort:nnnn DestPort:9000 Seq:1415566393 Ack:1336099544 Flags:32784 WindowSize:229 Checksum:59263 UrgentPtr:0}, flag: ACK , data: [], addr: C.C.C.C
    conn S.S.S.S:9000-C.C.C.C:nnnn: ACK connection established
    conn S.S.S.S:9000-C.C.C.C:nnnn: wrote 1 invalid packets with seq: [seq + 100000 , seq + 100000 + 9[ (seq 1336099544)
    conn S.S.S.S:9000-C.C.C.C:nnnn: tcp packet: &{SrcPort:nnnn DestPort:9000 Seq:1415566393 Ack:0 Flags:20484 WindowSize:0 Checksum:21080 UrgentPtr:0}, flag: RST , data: [], addr: C.C.C.C
    conn S.S.S.S:9000-C.C.C.C:nnnn: RST received
    panic: RST received
    ... exits
    

Note that, when lauching the server as above and launching the client without docker, everything goes fine
(i.e. the client will see a out-of-window packet but will simply drop it). Launch the client without docker as follow:

    C.C.C.C$ make SERVER_HOST=C.C.C.C docker-issue-1090-client-ok
    # Client on host
    set -x; while true; do nc -w 3 gnbcxd0072 9000; sleep 1; done
    + true
    + nc -w 3 gnbcxd0072 9000
    ...

This time on the server side, the invalid packet is still forged, but the connection terminates normally after
the client timeout (3 secs as specified in the client with `nc -w 3 ...`):

    S.S.S.S$ ...
    conn S.S.S.S:9000-C.C.C.C:nnnn: wrote 1 invalid packets with seq: [seq + 100000 , seq + 100000 + 9[ (seq 3236006542)
    conn S.S.S.S:9000-C.C.C.C:nnnn: ...
    conn S.S.S.S:9000-C.C.C.C:nnnn: normal temination

If the routing client side is fixed to DROP invalid packets in the INPUT chain, the dockerized client should
also run normally and the server should not see any RST.

In order to fix on the client host, one can add the follwing to the iptables:

    C.C.C.C$ sudo iptables list
    Chain INPUT (policy ACCEPT)
    target     prot opt source               destination         
    ....
    
    Chain FORWARD (policy DROP)
    ...

    C.C.C.C$ sudo iptables -N MY-WORKAROUND
    C.C.C.C$ sudo iptables -A MY-WORKAROUND -m conntrack --ctstate INVALID -j DROP
    C.C.C.C$ sudo iptables -A MY-WORKAROUND -j RETURN
    C.C.C.C$ sudo iptables -I INPUT 1 -j MY-WORKAROUND
    C.C.C.C$ sudo iptables --list
    Chain INPUT (policy ACCEPT)
    target     prot opt source               destination         
    MY-WORKAROUND  all  --  anywhere             anywhere            
    ....
    Chain MY-WORKAROUND (1 references)
    target     prot opt source               destination         
    DROP       all  --  anywhere             anywhere             ctstate INVALID
    RETURN     all  --  anywhere             anywhere            

After adding the MY-WORKAROUND chain as above, re-run the experiment. It should fix the issue as an invalid packet (including
out-of-window packets) will be discarded instead of being passed to the host interface.

In order to remote the My-WORKAROUND chain, remove the jump from the INPUT chain and discard the chain with:

    C.C.C.C$ sudo iptables -D INPUT -j MY-WORKAROUND
    C.C.C.C$ sudo iptables -F MY-WORKAROUND
    C.C.C.C$ sudo iptables -X MY-WORKAROUND


## Howto reproduce k8s issue #74839

Note from guillon: actually I did not yet try the issue fix over kubernetes, still TODO...

1. Create a cluster with at least 2 nodes.
1. Deploy the app.
```console
kubectl create -f https://raw.githubusercontent.com/anfernee/k8s-issue-74839/master/deploy.yaml
```

1. Check if the server crashed
```console
$ kubectl get pods
NAME                           READY   STATUS             RESTARTS   AGE
boom-server-59945555cd-8rwqk   0/1     CrashLoopBackOff   4          2m
startup-script                 1/1     Running            0          2m
```

## Reference

Docker issue: https://github.com/docker/libnetwork/issues/1090

Docker pull request: https://github.com/docker/libnetwork/pull/2275

Kubernetes issue: https://github.com/kubernetes/kubernetes/issues/74839


