"""Shell completion scripts for bash, zsh and fish."""

from __future__ import annotations

from typing import Dict


BASH_COMPLETION_SCRIPT: str = """# bash completion for shortcut

_shortcut() {
    local cur prev words cword
    if type _init_completion &>/dev/null; then
        _init_completion || return
    else
        COMPREPLY=()
        cur="${COMP_WORDS[COMP_CWORD]}"
        prev="${COMP_WORDS[COMP_CWORD-1]}"
    fi

    local subcommands="run r new list show location rm promote update init completion help"
    local name_commands="run r show location rm promote"

    if [ "${COMP_CWORD:-1}" -eq 1 ]; then
        COMPREPLY=( $(compgen -W "${subcommands}" -- "$cur") )
        return 0
    fi

    # What's before the cursor, leaving out flags: nothing yet, or a namespace
    local cmd="${COMP_WORDS[1]}" i
    local -a typed=()
    for (( i = 2; i < COMP_CWORD; i++ )); do
        [[ "${COMP_WORDS[i]}" == -* ]] || typed+=("${COMP_WORDS[i]}")
    done

    case " ${name_commands} " in
        *" ${cmd} "*)
            if [ "${#typed[@]}" -eq 0 ] && [[ "$cur" == -* ]]; then
                local flags="--global"
                case "$cmd" in
                    run|r) flags="--global -y" ;;
                    rm) flags="--global --local --namespace -y" ;;
                    promote) flags="--as -y --force" ;;
                    show) flags="--global --json" ;;
                esac
                COMPREPLY=( $(compgen -W "${flags}" -- "$cur") )
            elif [ "${#typed[@]}" -le 1 ]; then
                local names
                names=$(shortcut list --names ${typed[0]} 2>/dev/null)
                COMPREPLY=( $(compgen -W "${names}" -- "$cur") )
            fi
            return 0
            ;;
    esac

    case "$prev" in
        init)
            COMPREPLY=( $(compgen -W "--global --replace -y" -- "$cur") )
            return 0
            ;;
        completion)
            COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") )
            return 0
            ;;
    esac
}

complete -F _shortcut shortcut
"""

ZSH_COMPLETION_SCRIPT: str = """# zsh completion for shortcut (load after compinit)

_shortcut() {
    local -a subcommands
    subcommands=(run r new list show location rm promote update init completion help)

    if (( CURRENT == 2 )); then
        compadd -- $subcommands
        return
    fi

    case $words[2] in
        run|r|show|location|rm|promote)
            local -a typed
            typed=(${words[3,CURRENT-1]:#-*})
            if (( $#typed == 0 )) && [[ $PREFIX == -* ]]; then
                case $words[2] in
                    run|r) compadd -- --global -y ;;
                    rm) compadd -- --global --local --namespace -y ;;
                    show) compadd -- --global --json ;;
                    promote) compadd -- --as -y --force ;;
                    *) compadd -- --global ;;
                esac
            elif (( $#typed <= 1 )); then
                compadd -- ${(f)"$(shortcut list --names $typed[1] 2>/dev/null)"}
            fi
            ;;
        init)
            compadd -- --global --replace -y
            _files -/
            ;;
        completion)
            (( CURRENT == 3 )) && compadd -- bash zsh fish
            ;;
    esac
}

compdef _shortcut shortcut
"""

FISH_COMPLETION_SCRIPT: str = """# fish completion for shortcut

function __shortcut_names
    set -l typed (string match -v -- '-*' (commandline -opc)[3..-1])
    if test (count $typed) -le 1
        shortcut list --names $typed 2>/dev/null
    end
end

complete -c shortcut -f
complete -c shortcut -n __fish_use_subcommand -a "run r new list show location rm promote update init completion help"
complete -c shortcut -n "__fish_seen_subcommand_from run r show location rm promote" -a "(__shortcut_names)"
complete -c shortcut -n "__fish_seen_subcommand_from run r show location rm" -l global
complete -c shortcut -n "__fish_seen_subcommand_from run r rm promote" -s y
complete -c shortcut -n "__fish_seen_subcommand_from promote" -l as -r
complete -c shortcut -n "__fish_seen_subcommand_from promote" -l force
complete -c shortcut -n "__fish_seen_subcommand_from rm" -l local
complete -c shortcut -n "__fish_seen_subcommand_from rm" -l namespace -r
complete -c shortcut -n "__fish_seen_subcommand_from show" -l json
complete -c shortcut -n "__fish_seen_subcommand_from init" -F -l global
complete -c shortcut -n "__fish_seen_subcommand_from init" -s y
complete -c shortcut -n "__fish_seen_subcommand_from init" -l replace
complete -c shortcut -n "__fish_seen_subcommand_from completion" -a "bash zsh fish"
"""

COMPLETION_SCRIPTS: Dict[str, str] = {
    "bash": BASH_COMPLETION_SCRIPT,
    "zsh": ZSH_COMPLETION_SCRIPT,
    "fish": FISH_COMPLETION_SCRIPT,
}
