Summary:    Amazon CloudWatch Agent
Name:       amazon-cloudwatch-agent
Version:    %{AGENT_VERSION}
Release:    1
License:    MIT License. Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
Group:      Applications/CloudWatch-Agent

BuildRoot: %{_tmppath}/%{name}-%{version}-%{release}-buildroot-%(%{__id_u} -n)
Source:     amazon-cloudwatch-agent.tar.gz

%define _enable_debug_packages 0
%define debug_package %{nil}
%define _source_payload w6.gzdio
%define _binary_payload w6.gzdio
%define __jar_repack %{nil}

%prep
%setup -c %{name}-%{version}

%description
This package provides daemon of Amazon CloudWatch Agent

%install

rm -rf $RPM_BUILD_ROOT
mkdir $RPM_BUILD_ROOT
cp -r %{_topdir}/BUILD/%{name}-%{version}/*  $RPM_BUILD_ROOT/

############################# create the symbolic links
# bin
mkdir -p ${RPM_BUILD_ROOT}/usr/bin
ln -f -s /opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl ${RPM_BUILD_ROOT}/usr/bin/amazon-cloudwatch-agent-ctl
# etc
mkdir -p ${RPM_BUILD_ROOT}/etc/amazon
ln -f -s /opt/aws/amazon-cloudwatch-agent/etc ${RPM_BUILD_ROOT}/etc/amazon/amazon-cloudwatch-agent
# log
mkdir -p ${RPM_BUILD_ROOT}/var/log/amazon
ln -f -s /opt/aws/amazon-cloudwatch-agent/logs ${RPM_BUILD_ROOT}/var/log/amazon/amazon-cloudwatch-agent
# pid
mkdir -p ${RPM_BUILD_ROOT}/var/run/amazon
ln -f -s /opt/aws/amazon-cloudwatch-agent/var ${RPM_BUILD_ROOT}/var/run/amazon/amazon-cloudwatch-agent

%files
%dir /opt/aws/amazon-cloudwatch-agent
%dir /opt/aws/amazon-cloudwatch-agent/bin
%dir /opt/aws/amazon-cloudwatch-agent/doc
%dir /opt/aws/amazon-cloudwatch-agent/etc
%dir /opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.d
%dir /opt/aws/amazon-cloudwatch-agent/logs
%dir /opt/aws/amazon-cloudwatch-agent/var
/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent
/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl
/opt/aws/amazon-cloudwatch-agent/bin/CWAGENT_VERSION
/opt/aws/amazon-cloudwatch-agent/bin/config-translator
/opt/aws/amazon-cloudwatch-agent/bin/config-downloader
/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-config-wizard
/opt/aws/amazon-cloudwatch-agent/bin/start-amazon-cloudwatch-agent
%attr(700, -, -) /opt/aws/amazon-cloudwatch-agent/bin/workload-discovery
/opt/aws/amazon-cloudwatch-agent/bin/opentelemetry-jmx-metrics.jar
/opt/aws/amazon-cloudwatch-agent/doc/amazon-cloudwatch-agent-schema.json
%config(noreplace) /opt/aws/amazon-cloudwatch-agent/etc/common-config.toml
%dir /opt/aws/amazon-cloudwatch-agent/etc/ebpf
%config(noreplace) /opt/aws/amazon-cloudwatch-agent/etc/ebpf/obi.yaml
/opt/aws/amazon-cloudwatch-agent/etc/ebpf/systemd-dropin.conf
/opt/aws/amazon-cloudwatch-agent/LICENSE
/opt/aws/amazon-cloudwatch-agent/NOTICE

/opt/aws/amazon-cloudwatch-agent/THIRD-PARTY-LICENSES
/opt/aws/amazon-cloudwatch-agent/RELEASE_NOTES
/etc/init/amazon-cloudwatch-agent.conf
/etc/systemd/system/amazon-cloudwatch-agent.service

/usr/bin/amazon-cloudwatch-agent-ctl
/etc/amazon/amazon-cloudwatch-agent
/var/log/amazon/amazon-cloudwatch-agent
/var/run/amazon/amazon-cloudwatch-agent

%pre
# Stop the agent before upgrades.
if [ $1 -ge 2 ]; then
    if [ -x /opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl ]; then
        /opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a prep-restart
        /opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a stop
    fi
fi

# eBPF is opt-in (marker etc/ebpf/enabled + drop-in installed by
# "amazon-cloudwatch-agent-ctl -a enable-ebpf"). Hosts upgrading from the beta
# versions where eBPF was always on (the RPM owned service.d/ebpf.conf) keep it on.
if [ $1 -ge 2 ] && [ -f /etc/systemd/system/amazon-cloudwatch-agent.service.d/ebpf.conf ]; then
    mkdir -p /opt/aws/amazon-cloudwatch-agent/etc/ebpf
    touch /opt/aws/amazon-cloudwatch-agent/etc/ebpf/enabled
fi

if ! grep "^cwagent:" /etc/group >/dev/null 2>&1; then
    groupadd -r cwagent >/dev/null 2>&1
    echo "create group cwagent, result: $?"
fi

if ! id cwagent >/dev/null 2>&1; then
    useradd -r -M cwagent -d /home/cwagent -g cwagent -c "Cloudwatch Agent" -s $(test -x /sbin/nologin && echo /sbin/nologin || (test -x /usr/sbin/nologin && echo /usr/sbin/nologin || (test -x /bin/false && echo /bin/false || echo /bin/sh))) >/dev/null 2>&1
    echo "create user cwagent, result: $?"
fi

%preun
# Stop the agent after uninstall
if [ $1 -eq 0 ] ; then
    if [ -x /opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl ]; then
        /opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a preun
    fi
fi

%postun
# Full erase: remove the eBPF opt-in state, which the RPM does not own.
if [ $1 -eq 0 ]; then
    rm -f /opt/aws/amazon-cloudwatch-agent/etc/ebpf/enabled
    rm -f /etc/systemd/system/amazon-cloudwatch-agent.service.d/ebpf.conf
    rmdir /etc/systemd/system/amazon-cloudwatch-agent.service.d 2>/dev/null || :
    if command -v systemctl >/dev/null 2>&1; then
        systemctl daemon-reload >/dev/null 2>&1 || :
    fi
fi

%posttrans
# eBPF opted in: (re)install the systemd drop-in from the template. On an upgrade
# from an always-on version the old package's erase removed its ebpf.conf.
if [ -f /opt/aws/amazon-cloudwatch-agent/etc/ebpf/enabled ] && [ -f /opt/aws/amazon-cloudwatch-agent/etc/ebpf/systemd-dropin.conf ]; then
    mkdir -p /etc/systemd/system/amazon-cloudwatch-agent.service.d
    cp -f /opt/aws/amazon-cloudwatch-agent/etc/ebpf/systemd-dropin.conf /etc/systemd/system/amazon-cloudwatch-agent.service.d/ebpf.conf
    if command -v systemctl >/dev/null 2>&1; then
        systemctl daemon-reload >/dev/null 2>&1 || :
    fi
fi
# restart agent after upgrade
if [ -x /opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl ]; then
    /opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a cond-restart
fi

%clean