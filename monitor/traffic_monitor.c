#include <arpa/inet.h>
#include <errno.h>
#include <net/ethernet.h>
#include <netinet/ip.h>
#include <netinet/ip6.h>
#include <pcap.h>
#include <pthread.h>
#include <signal.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <time.h>
#include <unistd.h>

#define MAX_FLOWS 2048
#define WINDOW_SECONDS 40
#define DEFAULT_SNAPLEN 1600

typedef struct {
    char src[INET6_ADDRSTRLEN];
    char dst[INET6_ADDRSTRLEN];
    char proto[8];
    uint64_t packets;
    uint64_t bytes;
    uint64_t peak_bps;
    uint64_t current_bps;
    uint64_t buckets[WINDOW_SECONDS];
    time_t bucket_time[WINDOW_SECONDS];
    time_t last_seen;
    bool used;
} flow_t;

static flow_t flows[MAX_FLOWS];
static pthread_mutex_t flows_lock = PTHREAD_MUTEX_INITIALIZER;
static volatile sig_atomic_t stop_flag = 0;
static pcap_t *pcap_handle = NULL;
static const char *output_path = "/tmp/traffic_stats.json";
static int print_interval = 2;
static bool quiet = false;

static void usage(const char *prog) {
    fprintf(stderr,
            "Usage: %s -i <iface> [-o /tmp/traffic_stats.json] [-f bpf] [-p interval] [-q]\n"
            "  -i  capture interface, for example eth0 or br-lan\n"
            "  -o  JSON output path\n"
            "  -f  optional libpcap BPF filter, for example \"ip or ip6\"\n"
            "  -p  terminal print interval seconds, default 2\n"
            "  -q  quiet mode, only write JSON\n",
            prog);
}

static uint64_t now_millis(void) {
    struct timeval tv;
    gettimeofday(&tv, NULL);
    return (uint64_t)tv.tv_sec * 1000ULL + (uint64_t)tv.tv_usec / 1000ULL;
}

static const char *proto_name(uint8_t proto) {
    switch (proto) {
        case IPPROTO_TCP:
            return "tcp";
        case IPPROTO_UDP:
            return "udp";
        case IPPROTO_ICMP:
        case IPPROTO_ICMPV6:
            return "icmp";
        default:
            return "other";
    }
}

static int find_or_create_flow(const char *src, const char *dst, const char *proto) {
    int free_slot = -1;
    for (int i = 0; i < MAX_FLOWS; i++) {
        if (flows[i].used) {
            if (strcmp(flows[i].src, src) == 0 && strcmp(flows[i].dst, dst) == 0 && strcmp(flows[i].proto, proto) == 0) {
                return i;
            }
        } else if (free_slot < 0) {
            free_slot = i;
        }
    }

    if (free_slot >= 0) {
        flow_t *f = &flows[free_slot];
        memset(f, 0, sizeof(*f));
        snprintf(f->src, sizeof(f->src), "%s", src);
        snprintf(f->dst, sizeof(f->dst), "%s", dst);
        snprintf(f->proto, sizeof(f->proto), "%s", proto);
        f->used = true;
        return free_slot;
    }

    int oldest = 0;
    for (int i = 1; i < MAX_FLOWS; i++) {
        if (flows[i].last_seen < flows[oldest].last_seen) {
            oldest = i;
        }
    }
    flow_t *f = &flows[oldest];
    memset(f, 0, sizeof(*f));
    snprintf(f->src, sizeof(f->src), "%s", src);
    snprintf(f->dst, sizeof(f->dst), "%s", dst);
    snprintf(f->proto, sizeof(f->proto), "%s", proto);
    f->used = true;
    return oldest;
}

static void add_bytes_to_flow(const char *src, const char *dst, const char *proto, uint32_t bytes, time_t sec) {
    pthread_mutex_lock(&flows_lock);
    int idx = find_or_create_flow(src, dst, proto);
    flow_t *f = &flows[idx];
    int bucket = (int)(sec % WINDOW_SECONDS);
    if (f->bucket_time[bucket] != sec) {
        f->bucket_time[bucket] = sec;
        f->buckets[bucket] = 0;
    }
    f->packets += 1;
    f->bytes += bytes;
    f->buckets[bucket] += bytes;
    f->last_seen = sec;
    pthread_mutex_unlock(&flows_lock);
}

static void packet_handler(unsigned char *user, const struct pcap_pkthdr *header, const unsigned char *packet) {
    (void)user;
    if (header->caplen < sizeof(struct ether_header)) {
        return;
    }

    const struct ether_header *eth = (const struct ether_header *)packet;
    uint16_t ether_type = ntohs(eth->ether_type);
    const unsigned char *payload = packet + sizeof(struct ether_header);
    uint32_t payload_len = header->caplen - sizeof(struct ether_header);
    char src[INET6_ADDRSTRLEN] = {0};
    char dst[INET6_ADDRSTRLEN] = {0};
    const char *proto = "other";

    if (ether_type == ETHERTYPE_VLAN && payload_len >= 4) {
        ether_type = ntohs(*(const uint16_t *)(payload + 2));
        payload += 4;
        payload_len -= 4;
    }

    if (ether_type == ETHERTYPE_IP) {
        if (payload_len < sizeof(struct ip)) {
            return;
        }
        const struct ip *ip = (const struct ip *)payload;
        inet_ntop(AF_INET, &ip->ip_src, src, sizeof(src));
        inet_ntop(AF_INET, &ip->ip_dst, dst, sizeof(dst));
        proto = proto_name(ip->ip_p);
    } else if (ether_type == ETHERTYPE_IPV6) {
        if (payload_len < sizeof(struct ip6_hdr)) {
            return;
        }
        const struct ip6_hdr *ip6 = (const struct ip6_hdr *)payload;
        inet_ntop(AF_INET6, &ip6->ip6_src, src, sizeof(src));
        inet_ntop(AF_INET6, &ip6->ip6_dst, dst, sizeof(dst));
        proto = proto_name(ip6->ip6_nxt);
    } else {
        return;
    }

    add_bytes_to_flow(src, dst, proto, header->len, header->ts.tv_sec);
}

static uint64_t sum_window(const flow_t *f, time_t now, int seconds) {
    uint64_t sum = 0;
    for (int i = 0; i < WINDOW_SECONDS; i++) {
        if (f->bucket_time[i] > 0 && now - f->bucket_time[i] >= 0 && now - f->bucket_time[i] < seconds) {
            sum += f->buckets[i];
        }
    }
    return sum;
}

static void json_escape(FILE *fp, const char *s) {
    fputc('"', fp);
    for (; *s; s++) {
        if (*s == '"' || *s == '\\') {
            fputc('\\', fp);
        }
        fputc(*s, fp);
    }
    fputc('"', fp);
}

static int write_stats_json(void) {
    char tmp[512];
    snprintf(tmp, sizeof(tmp), "%s.tmp.%ld", output_path, (long)getpid());

    FILE *fp = fopen(tmp, "w");
    if (!fp) {
        fprintf(stderr, "open %s failed: %s\n", tmp, strerror(errno));
        return -1;
    }

    time_t now = time(NULL);
    pthread_mutex_lock(&flows_lock);
    fprintf(fp, "{\"ok\":true,\"timestamp\":%llu,\"flows\":[", (unsigned long long)now_millis());
    bool first = true;
    for (int i = 0; i < MAX_FLOWS; i++) {
        flow_t *f = &flows[i];
        if (!f->used || now - f->last_seen > 300) {
            continue;
        }

        uint64_t one = sum_window(f, now, 1);
        uint64_t two = sum_window(f, now, 2) / 2;
        uint64_t ten = sum_window(f, now, 10) / 10;
        uint64_t forty = sum_window(f, now, 40) / 40;
        f->current_bps = one;
        if (one > f->peak_bps) {
            f->peak_bps = one;
        }

        if (!first) {
            fputc(',', fp);
        }
        first = false;
        fprintf(fp, "{\"src_ip\":");
        json_escape(fp, f->src);
        fprintf(fp, ",\"dst_ip\":");
        json_escape(fp, f->dst);
        fprintf(fp, ",\"proto\":");
        json_escape(fp, f->proto);
        fprintf(fp,
                ",\"packets\":%llu,\"bytes\":%llu,\"current_bps\":%llu,\"peak_bps\":%llu,\"avg_2s_bps\":%llu,\"avg_10s_bps\":%llu,\"avg_40s_bps\":%llu}",
                (unsigned long long)f->packets,
                (unsigned long long)f->bytes,
                (unsigned long long)f->current_bps,
                (unsigned long long)f->peak_bps,
                (unsigned long long)two,
                (unsigned long long)ten,
                (unsigned long long)forty);
    }
    fprintf(fp, "]}\n");
    pthread_mutex_unlock(&flows_lock);

    if (fclose(fp) != 0) {
        return -1;
    }
    if (rename(tmp, output_path) != 0) {
        fprintf(stderr, "rename %s to %s failed: %s\n", tmp, output_path, strerror(errno));
        unlink(tmp);
        return -1;
    }
    return 0;
}

static void print_table(void) {
    time_t now = time(NULL);
    pthread_mutex_lock(&flows_lock);
    printf("\033[2J\033[H");
    printf("OpenWrt traffic monitor - %ld\n", (long)now);
    printf("%-40s %-40s %-6s %10s %12s %12s %12s %12s\n", "SRC", "DST", "PROTO", "PACKETS", "BYTES", "CUR B/s", "PEAK B/s", "AVG10 B/s");
    int rows = 0;
    for (int i = 0; i < MAX_FLOWS && rows < 30; i++) {
        flow_t *f = &flows[i];
        if (!f->used || now - f->last_seen > 60) {
            continue;
        }
        uint64_t one = sum_window(f, now, 1);
        uint64_t ten = sum_window(f, now, 10) / 10;
        if (one > f->peak_bps) {
            f->peak_bps = one;
        }
        printf("%-40s %-40s %-6s %10llu %12llu %12llu %12llu %12llu\n",
               f->src,
               f->dst,
               f->proto,
               (unsigned long long)f->packets,
               (unsigned long long)f->bytes,
               (unsigned long long)one,
               (unsigned long long)f->peak_bps,
               (unsigned long long)ten);
        rows++;
    }
    pthread_mutex_unlock(&flows_lock);
    fflush(stdout);
}

static void *stats_thread(void *arg) {
    (void)arg;
    int tick = 0;
    while (!stop_flag) {
        sleep(1);
        write_stats_json();
        tick++;
        if (!quiet && tick % print_interval == 0) {
            print_table();
        }
    }
    write_stats_json();
    return NULL;
}

static void on_signal(int signo) {
    (void)signo;
    stop_flag = 1;
    if (pcap_handle) {
        pcap_breakloop(pcap_handle);
    }
}

int main(int argc, char **argv) {
    const char *iface = NULL;
    const char *filter = "ip or ip6";
    int opt;
    while ((opt = getopt(argc, argv, "i:o:f:p:qh")) != -1) {
        switch (opt) {
            case 'i':
                iface = optarg;
                break;
            case 'o':
                output_path = optarg;
                break;
            case 'f':
                filter = optarg;
                break;
            case 'p':
                print_interval = atoi(optarg);
                if (print_interval < 1) {
                    print_interval = 1;
                }
                break;
            case 'q':
                quiet = true;
                break;
            case 'h':
            default:
                usage(argv[0]);
                return opt == 'h' ? 0 : 1;
        }
    }

    if (!iface) {
        usage(argv[0]);
        return 1;
    }

    signal(SIGINT, on_signal);
    signal(SIGTERM, on_signal);

    char errbuf[PCAP_ERRBUF_SIZE] = {0};
    pcap_handle = pcap_open_live(iface, DEFAULT_SNAPLEN, 1, 1000, errbuf);
    if (!pcap_handle) {
        fprintf(stderr, "pcap_open_live(%s) failed: %s\n", iface, errbuf);
        return 1;
    }

    struct bpf_program fp;
    if (pcap_compile(pcap_handle, &fp, filter, 1, PCAP_NETMASK_UNKNOWN) != 0) {
        fprintf(stderr, "pcap_compile failed: %s\n", pcap_geterr(pcap_handle));
        pcap_close(pcap_handle);
        return 1;
    }
    if (pcap_setfilter(pcap_handle, &fp) != 0) {
        fprintf(stderr, "pcap_setfilter failed: %s\n", pcap_geterr(pcap_handle));
        pcap_freecode(&fp);
        pcap_close(pcap_handle);
        return 1;
    }
    pcap_freecode(&fp);

    pthread_t tid;
    if (pthread_create(&tid, NULL, stats_thread, NULL) != 0) {
        fprintf(stderr, "pthread_create failed\n");
        pcap_close(pcap_handle);
        return 1;
    }

    int rc = pcap_loop(pcap_handle, -1, packet_handler, NULL);
    if (rc == -1) {
        fprintf(stderr, "pcap_loop failed: %s\n", pcap_geterr(pcap_handle));
    }
    stop_flag = 1;
    pthread_join(tid, NULL);
    pcap_close(pcap_handle);
    return rc == -1 ? 1 : 0;
}
