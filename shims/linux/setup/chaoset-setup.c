// Chaoset Linux 设置启动器（chaoset-setup）。
//
// 设置界面的唯一实现是 Slint 统一设置中心 shims/settings（构建产物
// chaoset-settings）。本文件只做进程启动：找到它、把参数原样转交、用 execv
// 替换本进程。不链接 GTK、不含任何 UI 代码，也**不回退**到任何原生设置窗口。
//
// 查找顺序：PATH → /usr/bin/chaoset-settings → 本可执行文件同目录。
// 程序名保持 chaoset-setup：ibus 组件描述 component/chaoset.xml 的 <setup>
// 入口（ibus-setup 的「首选项」按钮）与桌面入口都指向它。
//
// 找不到时按约定在 stderr 报「未找到统一设置中心（chaoset-settings）」并 exit(1)，
// 让缺包问题立刻暴露，而不是弹出一个功能残缺的旧设置窗。

/* 特性测试宏（必须在任何 include 之前）：realpath 在 glibc 里由
 * _XOPEN_SOURCE >= 500 才声明（man 7 feature_test_macros），execv/readlink/
 * access 等由 _POSIX_C_SOURCE 暴露；两个都定义以覆盖 -std=c11 严格模式。 */
#ifndef _XOPEN_SOURCE
#define _XOPEN_SOURCE 700
#endif
#ifndef _POSIX_C_SOURCE
#define _POSIX_C_SOURCE 200809L
#endif

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#define SETTINGS_NAME "chaoset-settings"
/* 发行包里的实际安装位置：install.sh / deb / Arch 包都装到 /usr/bin。 */
#define SETTINGS_SYSTEM_PATH "/usr/bin/" SETTINGS_NAME
#define MAX_PATH_LEN 4096

/* 拼接 dir + "/" + name 到 out；放不下返回 -1。 */
static int join_path(char *out, size_t cap, const char *dir, const char *name) {
    size_t dir_len = strlen(dir);
    size_t name_len = strlen(name);
    int need_sep = dir_len > 0 && dir[dir_len - 1] != '/';
    if (dir_len + (size_t)need_sep + name_len + 1 > cap) {
        return -1;
    }
    memcpy(out, dir, dir_len);
    size_t pos = dir_len;
    if (need_sep) {
        out[pos++] = '/';
    }
    memcpy(out + pos, name, name_len + 1);
    return 0;
}

/* 取本可执行文件所在目录（保留结尾 '/'，join_path 两种都接受）；成功返回 0。
 * 优先 /proc/self/exe（Linux 下与 argv[0] 无关，最可靠）；
 * 取不到再回退 POSIX 的 realpath(argv[0])（仅 argv[0] 自带路径时可用）。 */
static int self_directory(char *dir, size_t cap, const char *argv0) {
    char resolved[MAX_PATH_LEN];
    ssize_t len = readlink("/proc/self/exe", resolved, sizeof resolved - 1);
    if (len > 0 && (size_t)len < sizeof resolved - 1) {
        resolved[len] = '\0';
    } else if (argv0 != NULL && strchr(argv0, '/') != NULL) {
        char *abs = realpath(argv0, NULL);
        if (abs == NULL) {
            return -1;
        }
        len = (ssize_t)strlen(abs);
        if ((size_t)len >= sizeof resolved) {
            free(abs);
            return -1;
        }
        memcpy(resolved, abs, (size_t)len + 1);
        free(abs);
    } else {
        return -1;
    }

    char *slash = strrchr(resolved, '/');
    if (slash == NULL) {
        return -1;
    }
    slash[1] = '\0';
    if (strlen(resolved) >= cap) {
        return -1;
    }
    memcpy(dir, resolved, strlen(resolved) + 1); /* 含结尾 '/'，join_path 会复用 */
    return 0;
}

/* 在 PATH 各目录里找 name；跳过空项（不隐式搜索当前目录）。成功返回 0。 */
static int find_in_path(const char *name, char *found, size_t cap) {
    const char *path = getenv("PATH");
    if (path == NULL) {
        return -1;
    }
    while (*path != '\0') {
        const char *sep = strchr(path, ':');
        size_t dir_len = sep != NULL ? (size_t)(sep - path) : strlen(path);
        if (dir_len > 0 && dir_len < MAX_PATH_LEN) {
            char entry[MAX_PATH_LEN];
            memcpy(entry, path, dir_len);
            entry[dir_len] = '\0';
            if (join_path(found, cap, entry, name) == 0 && access(found, X_OK) == 0) {
                return 0;
            }
        }
        if (sep == NULL) {
            break;
        }
        path = sep + 1;
    }
    return -1;
}

/* 用 candidate 替换本进程（参数原样转交，argv[0] 改成真实程序路径）。 */
static void exec_settings(const char *candidate, char **argv) {
    argv[0] = (char *)candidate; /* main 的 argv 字符串指针可写；execv 读取时有效 */
    execv(candidate, argv);
}

int main(int argc, char **argv) {
    (void)argc;
    char candidate[MAX_PATH_LEN];

    if (find_in_path(SETTINGS_NAME, candidate, sizeof candidate) == 0) {
        exec_settings(candidate, argv);
    }
    if (access(SETTINGS_SYSTEM_PATH, X_OK) == 0) {
        exec_settings(SETTINGS_SYSTEM_PATH, argv);
    }
    char dir[MAX_PATH_LEN];
    if (self_directory(dir, sizeof dir, argv[0]) == 0 &&
        join_path(candidate, sizeof candidate, dir, SETTINGS_NAME) == 0 &&
        access(candidate, X_OK) == 0) {
        exec_settings(candidate, argv);
    }

    /* 三种查找都没启动成功：唯一原因 + 明确指引，绝不回退旧设置窗。 */
    fprintf(stderr, "chaoset-setup: 未找到统一设置中心（chaoset-settings）\n");
    fprintf(stderr, "chaoset-setup: 请安装 chaoset-settings（随发行包提供；源码树内先 make build-settings）\n");
    exit(1);
}
