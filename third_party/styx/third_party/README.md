# third_party

Proto 依赖文件，供 styx 及依赖 styx 的项目使用。

## 目录结构

```
third_party/
└── google/
    └── protobuf/
        └── duration.proto
```

## 使用方式

依赖 styx 的项目在 Makefile 中引用：

```makefile
STYX_PATH=$(shell go list -m -f '{{.Dir}}' gitee.com/linpbin/styx)

config:
	protoc --proto_path=./internal \
	       --proto_path=$(STYX_PATH) \
	       --proto_path=$(STYX_PATH)/third_party \
	       --go_out=paths=source_relative:./internal \
	       $(INTERNAL_PROTO_FILES)
```
